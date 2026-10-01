// Package github reads GitHub metadata and resolves action versions.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/wallentx/actions-snitch/internal/cache"
)

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	APIBase    string
	RawBase    string
	WebBase    string
	BadgeBase  string
	Token      string
	HTTP       Doer
	PublicHTTP Doer
	Cache      *cache.Store
}

func New(host, token string, store *cache.Store) *Client {
	base := "https://api.github.com/"
	if strings.HasSuffix(host, ".ghe.com") {
		base = "https://api." + host + "/"
	} else if host != "" && host != "github.com" {
		base = "https://" + host + "/api/v3/"
	}
	// Redirects are deliberately not followed with credentials. Pagination is
	// separately restricted to the configured API origin and prefix.
	api := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	public := &http.Client{Timeout: 30 * time.Second}
	return &Client{APIBase: base, RawBase: "https://raw.githubusercontent.com/", WebBase: "https://github.com/", BadgeBase: "https://dependabot-badges.githubapp.com/badges/compatibility_score", Token: token, HTTP: api, PublicHTTP: public, Cache: store}
}

func (c *Client) apiURL(endpoint string) (string, error) {
	base, err := url.Parse(c.APIBase)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if !u.IsAbs() {
		u = base.ResolveReference(u)
	}
	if u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || !strings.HasPrefix(u.Path, base.Path) {
		return "", errors.New("GitHub pagination left the configured API origin")
	}
	return u.String(), nil
}

const responseLimit = 16 << 20

func readResponse(response *http.Response) ([]byte, error) {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > responseLimit {
		return nil, errors.New("HTTP response exceeds size limit")
	}
	return b, nil
}

func (c *Client) request(ctx context.Context, endpoint string) ([]byte, http.Header, error) {
	u, err := c.apiURL(endpoint)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("GitHub request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	b, err := readResponse(response)
	return b, response.Header, err
}

func (c *Client) key(endpoint string) string {
	return "api-v1:" + c.APIBase + ":" + cache.Key(c.Token) + ":" + endpoint
}

func decode(b []byte, out any) error {
	if len(bytes.TrimSpace(b)) == 0 || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return errors.New("GitHub returned no data")
	}
	value := reflect.ValueOf(out)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errors.New("JSON destination must be a non-nil pointer")
	}
	temporary := reflect.New(value.Elem().Type())
	if err := json.Unmarshal(b, temporary.Interface()); err != nil {
		return err
	}
	value.Elem().Set(temporary.Elem())
	return nil
}

func (c *Client) JSON(ctx context.Context, endpoint string, out any) error {
	key := c.key(endpoint)
	if c.Cache != nil {
		if b, ok := c.Cache.Get(key); ok && decode(b, out) == nil && cacheable(out) {
			return nil
		}
	}
	b, _, err := c.request(ctx, endpoint)
	if err != nil {
		return err
	}
	if err := decode(b, out); err != nil {
		return fmt.Errorf("invalid GitHub response: %w", err)
	}
	if c.Cache != nil && cacheable(out) {
		_ = c.Cache.Put(key, b)
	}
	return nil
}

func (c *Client) Public(ctx context.Context, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.PublicHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	return readResponse(response)
}

func (c *Client) Raw(ctx context.Context, repo, ref, path string) ([]byte, error) {
	return c.Public(ctx, strings.TrimRight(c.RawBase, "/")+"/"+repo+"/"+url.PathEscape(ref)+"/"+path)
}

type Repository struct {
	DefaultBranch string `json:"default_branch"`
}
type Release struct {
	Tag  string `json:"tag_name"`
	Body string `json:"body"`
	URL  string `json:"html_url"`
}
type Tag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}
type Compare struct {
	Status  string          `json:"status"`
	Ahead   json.RawMessage `json:"ahead_by"`
	Behind  json.RawMessage `json:"behind_by"`
	Commits []Commit        `json:"commits"`
}
type Commit struct {
	SHA    string `json:"sha"`
	URL    string `json:"html_url"`
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

func (c *Client) Latest(ctx context.Context, repo string) (Release, error) {
	var r Release
	err := c.JSON(ctx, "repos/"+repo+"/releases/latest", &r)
	if err == nil && (r.Tag == "" || r.Tag == "null") {
		err = errors.New("release has no tag")
	}
	return r, err
}
func (c *Client) Repository(ctx context.Context, repo string) (Repository, error) {
	var r Repository
	err := c.JSON(ctx, "repos/"+repo, &r)
	if err == nil && (r.DefaultBranch == "" || r.DefaultBranch == "null") {
		err = errors.New("repository has no default branch")
	}
	return r, err
}
func (c *Client) Compare(ctx context.Context, repo, base, head string) (Compare, error) {
	var r Compare
	err := c.JSON(ctx, "repos/"+repo+"/compare/"+url.PathEscape(base)+"..."+url.PathEscape(head), &r)
	return r, err
}

func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		if len(fields) < 2 {
			continue
		}
		for _, field := range fields[1:] {
			if strings.TrimSpace(field) == `rel="next"` {
				return strings.Trim(strings.TrimSpace(fields[0]), "<>")
			}
		}
	}
	return ""
}

// Tags returns all pages or an error; partial pages cannot prove an exact match.
func (c *Client) Tags(ctx context.Context, repo string) ([]Tag, error) {
	endpoint := "repos/" + repo + "/tags?per_page=100"
	key := c.key("all-pages:" + endpoint)
	var result []Tag
	if c.Cache != nil {
		if b, ok := c.Cache.Get(key); ok && decode(b, &result) == nil {
			return result, nil
		}
	}
	seen := map[string]bool{}
	for endpoint != "" {
		u, err := c.apiURL(endpoint)
		if err != nil {
			return nil, err
		}
		if seen[u] {
			return nil, errors.New("GitHub pagination loop")
		}
		seen[u] = true
		b, header, err := c.request(ctx, u)
		if err != nil {
			return nil, err
		}
		var page []Tag
		if err := decode(b, &page); err != nil {
			return nil, err
		}
		result = append(result, page...)
		endpoint = nextLink(header.Get("Link"))
	}
	if result == nil {
		result = []Tag{}
	}
	if c.Cache != nil {
		b, err := json.Marshal(result)
		if err == nil {
			_ = c.Cache.Put(key, b)
		}
	}
	return result, nil
}

func cacheable(value any) bool {
	switch v := value.(type) {
	case *Repository:
		return v.DefaultBranch != "" && v.DefaultBranch != "null"
	case *Release:
		return v.Tag != "" && v.Tag != "null"
	case *Commit:
		return IsSHA(v.SHA)
	default:
		return true
	}
}
