package github

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/workflow"
)

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var versionPrefix = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:\.[0-9]+)?`)
var majorPattern = regexp.MustCompile(`^[0-9]+$`)
var stablePattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)$`)
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func IsSHA(s string) bool           { return shaPattern.MatchString(s) }
func VersionPrefix(s string) string { return versionPrefix.FindString(s) }
func VersionRef(s string) string {
	if strings.HasPrefix(s, "v") || IsSHA(s) || VersionPrefix(s) == "" {
		return s
	}
	return "v" + s
}
func Major(s string) string { return strings.SplitN(VersionPrefix(s), ".", 2)[0] }
func ReleaseURL(repo, version string) *string {
	if IsSHA(version) || VersionPrefix(version) == "" {
		return nil
	}
	return model.String("https://github.com/" + repo + "/releases/tag/" + VersionRef(version))
}

func (c *Client) SHA(ctx context.Context, repo, ref string) (string, error) {
	var result Commit
	if err := c.JSON(ctx, "repos/"+repo+"/commits/"+url.PathEscape(ref), &result); err != nil {
		return "", err
	}
	if !IsSHA(result.SHA) {
		return "", fmt.Errorf("invalid commit SHA")
	}
	return strings.ToLower(result.SHA), nil
}

// ExactTag prefers the earliest full stable version, never an ancestor tag.
func ExactTag(tags []Tag, sha string) string {
	var names []string
	for _, tag := range tags {
		if strings.EqualFold(tag.Commit.SHA, sha) {
			names = append(names, tag.Name)
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		a, b := stablePattern.FindStringSubmatch(names[i]), stablePattern.FindStringSubmatch(names[j])
		if (a != nil) != (b != nil) {
			return a != nil
		}
		if a != nil {
			for k := 1; k <= 3; k++ {
				left, right := strings.TrimLeft(a[k], "0"), strings.TrimLeft(b[k], "0")
				if len(left) != len(right) {
					return len(left) < len(right)
				}
				if left != right {
					return left < right
				}
			}
		}
		return names[i] < names[j]
	})
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

type Resolution struct {
	Finding     *model.Finding
	Warnings    []string
	Debug       []string
	Current     bool
	Unavailable bool
}

func (c *Client) Resolve(ctx context.Context, u workflow.Usage, pin bool) Resolution {
	result := Resolution{}
	warn := func(s string) Resolution { result.Warnings = append(result.Warnings, s); return result }
	if !u.Scannable() || !repositoryPattern.MatchString(u.Repository) {
		return result
	}
	release, err := c.Latest(ctx, u.Repository)
	latestRef := release.Tag
	if err != nil {
		result.Debug = append(result.Debug, "  Failed to fetch data from GitHub API: repos/"+u.Repository+"/releases/latest")
		repo, e := c.Repository(ctx, u.Repository)
		if e != nil {
			result.Debug = append(result.Debug, "  Failed to fetch data from GitHub API: repos/"+u.Repository)
			result.Unavailable = true
			return result
		}
		latestRef = repo.DefaultBranch
	}
	latest := strings.TrimPrefix(latestRef, "v")
	f := model.Finding{File: u.File, Line: u.Line, Action: u.Action, Repository: u.Repository, Current: u.Current, Latest: latest, UpdateRef: latestRef, CompatibilityScore: "Unknown"}
	needs := false
	if IsSHA(u.Current) {
		sha, err := c.SHA(ctx, u.Repository, latestRef)
		if err != nil {
			return warn(fmt.Sprintf("Unable to resolve %s@%s to a commit; skipping.", u.Repository, latestRef))
		}
		if strings.EqualFold(sha, u.Current) {
			result.Current = true
			return result
		}
		comparison, err := c.Compare(ctx, u.Repository, u.Current, sha)
		if err != nil {
			return warn(fmt.Sprintf("Unable to compare %s@%s with %s; skipping.", u.Repository, u.Current, sha))
		}
		switch comparison.Status {
		case "ahead":
		case "behind", "identical", "diverged":
			return warn(fmt.Sprintf("Skipping %s: latest target is %s relative to the current SHA.", u.Repository, comparison.Status))
		default:
			return warn(fmt.Sprintf("Invalid comparison for %s; skipping.", u.Repository))
		}
		distance, valid := positiveCount(comparison.Ahead)
		if !valid {
			return warn(fmt.Sprintf("Missing commit count for %s; skipping.", u.Repository))
		}
		f.CommitsSince = &distance
		f.LatestSHA = &sha
		f.UpdateRef = sha
		tags, err := c.Tags(ctx, u.Repository)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Unable to look up tags for %s@%s; showing commit distance only.", u.Repository, u.Current))
		} else {
			f.CurrentTag = model.String(ExactTag(tags, u.Current))
		}
		needs = true
	} else {
		currentPrefix, latestPrefix := VersionPrefix(u.Current), VersionPrefix(latest)
		if majorPattern.MatchString(u.Current) && !pin {
			major := Major(latest)
			if major == "" {
				return result
			}
			if u.Current == major {
				result.Current = true
				return result
			}
			f.Latest = major
			idx := strings.IndexAny(latestRef, "0123456789")
			prefix := ""
			if idx >= 0 {
				prefix = latestRef[:idx]
			}
			f.UpdateRef = prefix + major
		}
		needs = currentPrefix != "" && currentPrefix != latestPrefix
		if pin && (needs || (currentPrefix != "" && latestPrefix != "") || (release.Tag != "" && u.Ref == release.Tag)) {
			sha, err := c.SHA(ctx, u.Repository, latestRef)
			if err != nil {
				return warn(fmt.Sprintf("Unable to resolve %s@%s to a commit; skipping.", u.Repository, latestRef))
			}
			f.LatestSHA = &sha
			f.UpdateRef = sha
			needs = true
		}
	}
	if needs {
		f.ReleaseNotes = ReleaseURL(f.Repository, f.Latest)
		result.Finding = &f
	} else {
		result.Current = true
	}
	return result
}

func positiveCount(raw []byte) (int, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 128 || raw[0] < '0' || raw[0] > '9' {
		return 0, false
	}
	approx, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || approx <= 0 || approx > 9223372036854775808 {
		return 0, false
	}
	value, ok := new(big.Rat).SetString(string(raw))
	if !ok || !value.IsInt() || value.Sign() <= 0 || !value.Num().IsInt64() {
		return 0, false
	}
	n := value.Num().Int64()
	if strconv.IntSize == 32 && n > 2147483647 {
		return 0, false
	}
	return int(n), true
}
