package github

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var numericReleaseTag = regexp.MustCompile(`^v?[0-9]+(?:\.[0-9]+){0,2}$`)
var productReleaseTag = regexp.MustCompile(`^.+[-_/]v?[0-9]+(?:\.[0-9]+){1,2}(?:[-+].*)?$`)

// newestNumericRelease selects the action's numeric version family when the
// repository-wide latest release belongs to a named product. Pagination must
// finish successfully before a candidate can authorize an update.
func (c *Client) newestNumericRelease(ctx context.Context, repo string) (Release, error) {
	key := c.key("numeric-action-release-v1:" + repo)
	if c.Cache != nil {
		if data, ok := c.Cache.Get(key); ok {
			var cached Release
			if decode(data, &cached) == nil && eligibleNumericRelease(cached) {
				return cached, nil
			}
		}
	}
	endpoint := "repos/" + repo + "/releases?per_page=100"
	seen := map[string]bool{}
	var best Release
	for endpoint != "" {
		address, err := c.apiURL(endpoint)
		if err != nil {
			return Release{}, err
		}
		if seen[address] {
			return Release{}, errors.New("GitHub release pagination loop")
		}
		seen[address] = true
		data, headers, err := c.request(ctx, address)
		if err != nil {
			return Release{}, err
		}
		var releases []Release
		if err := decode(data, &releases); err != nil {
			return Release{}, err
		}
		for _, release := range releases {
			if !eligibleNumericRelease(release) {
				continue
			}
			if best.Tag == "" || compareNumericVersions(release.Tag, best.Tag) > 0 {
				best = release
				best.Body = ""
			}
		}
		endpoint = nextLink(headers.Get("Link"))
	}
	if best.Tag == "" {
		return Release{}, errors.New("no stable release matches the action's numeric version family")
	}
	if c.Cache != nil {
		if data, err := json.Marshal(best); err == nil {
			_ = c.Cache.Put(key, data)
		}
	}
	return best, nil
}

func eligibleNumericRelease(release Release) bool {
	return !release.Draft && !release.Prerelease && numericReleaseTag.MatchString(release.Tag)
}

func compareNumericVersions(a, b string) int {
	left, right := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < 3; i++ {
		x, y := "0", "0"
		if i < len(left) {
			x = left[i]
		}
		if i < len(right) {
			y = right[i]
		}
		if numericLess(x, y) {
			return -1
		}
		if numericLess(y, x) {
			return 1
		}
	}
	return 0
}
