package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/wallentx/actions-snitch/internal/compat"
	"gopkg.in/yaml.v3"
)

func (c *Client) Score(ctx context.Context, repo, current, latest string) any {
	key := "compat-v1:" + c.BadgeBase + ":" + repo + ":" + current + ":" + latest
	if c.Cache != nil {
		if b, ok := c.Cache.Get(key); ok {
			return compat.Score(string(b))
		}
	}
	query := url.Values{"dependency-name": {repo}, "package-manager": {"github_actions"}, "previous-version": {current}, "new-version": {latest}}
	b, err := c.Public(ctx, c.BadgeBase+"?"+query.Encode())
	var score any = "Unknown"
	if err == nil {
		score = compat.BadgeScore(string(b))
	}
	if c.Cache != nil {
		_ = c.Cache.Put(key, []byte(fmt.Sprint(score)))
	}
	return score
}

var slugSeparators = regexp.MustCompile(`[^a-z0-9]+`)

const verifiedSentence = "GitHub has manually verified the creator of the action as an official partner organization."

func (c *Client) Verified(ctx context.Context, repo string) bool {
	key := "verified-v1:" + c.RawBase + ":" + c.WebBase + ":" + c.APIBase + ":" + repo
	if c.Cache != nil {
		if b, ok := c.Cache.Get(key); ok && (string(b) == "true" || string(b) == "false") {
			return string(b) == "true"
		}
	}
	verified := false
	if metadata, err := c.Repository(ctx, repo); err == nil {
		for _, name := range []string{"action.yml", "action.yaml"} {
			definition, err := c.Raw(ctx, repo, metadata.DefaultBranch, name)
			if err != nil {
				continue
			}
			var action struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(definition, &action) != nil || action.Name == "" || action.Name == "null" {
				continue
			}
			slug := strings.Trim(slugSeparators.ReplaceAllString(strings.ToLower(action.Name), "-"), "-")
			if slug != "" {
				if page, err := c.Public(ctx, strings.TrimRight(c.WebBase, "/")+"/marketplace/actions/"+slug); err == nil {
					verified = strings.Contains(string(page), verifiedSentence)
				}
			}
			break
		}
	}

	if c.Cache != nil {
		_ = c.Cache.Put(key, []byte(fmt.Sprint(verified)))
	}
	return verified
}

func LimitCharacters(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}

func (c *Client) Definition(ctx context.Context, repo, ref, actionPath string) (string, error) {
	prefix := strings.TrimPrefix(actionPath, repo)
	prefix = strings.TrimPrefix(prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	for _, name := range []string{"action.yml", "action.yaml"} {
		if b, err := c.Raw(ctx, repo, ref, prefix+name); err == nil && len(b) > 0 {
			return LimitCharacters(string(b), 12000), nil
		}
	}
	return "", fmt.Errorf("action definition unavailable for %s@%s", actionPath, ref)
}

func (c *Client) Releases(ctx context.Context, repo string) ([]Release, error) {
	var releases []Release
	err := c.JSON(ctx, "repos/"+repo+"/releases?per_page=100", &releases)
	return releases, err
}

func (c *Client) ReleaseNotes(ctx context.Context, repo, current, latest string) string {
	for _, ref := range []string{VersionRef(latest), latest} {
		var release Release
		if c.JSON(ctx, "repos/"+repo+"/releases/tags/"+url.PathEscape(ref), &release) == nil && release.Body != "" && release.Body != "null" {
			return "## " + ref + "\n" + release.Body
		}
		if ref == latest {
			break
		}
	}
	from, to := Major(current), Major(latest)
	if from == "" || to == "" {
		return ""
	}
	releases, err := c.Releases(ctx, repo)
	if err != nil {
		return ""
	}
	var parts []string
	for _, r := range releases {
		major := Major(strings.TrimPrefix(r.Tag, "v"))
		if major != "" && numericLess(from, major) && !numericLess(to, major) {
			parts = append(parts, "## "+r.Tag+"\n"+r.Body)
		}
	}
	return strings.Join(parts, "\n")
}

func numericLess(a, b string) bool {
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func (c *Client) Changelog(ctx context.Context, repo string) (body, path, branch string) {
	branch = "main"
	if r, err := c.Repository(ctx, repo); err == nil {
		branch = r.DefaultBranch
	}
	for _, name := range []string{"CHANGELOG.md", "changelog.md", "CHANGES.md", "HISTORY.md"} {
		if b, err := c.Raw(ctx, repo, branch, name); err == nil && len(b) > 0 {
			lines := strings.Split(string(b), "\n")
			if len(lines) > 160 {
				lines = lines[:160]
			}
			return strings.TrimRight(strings.Join(lines, "\n"), "\n"), name, branch
		}
	}
	return "", "", branch
}

type Issue struct {
	Title string `json:"title"`
	URL   string `json:"html_url"`
	State string `json:"state"`
	Body  string `json:"body"`
}

func (c *Client) Issues(ctx context.Context, repo, version string) ([]Issue, error) {
	query := url.Values{"q": {`repo:` + repo + ` is:issue "` + VersionRef(version) + `"`}}
	var result struct {
		Items []Issue `json:"items"`
	}
	err := c.JSON(ctx, "search/issues?"+query.Encode(), &result)
	if len(result.Items) > 5 {
		result.Items = result.Items[:5]
	}
	return result.Items, err
}

func JSONText(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(b)
}
