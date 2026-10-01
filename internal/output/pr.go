package output

import (
	"context"
	"fmt"
	"strings"

	"github.com/wallentx/actions-snitch/internal/github"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/update"
)

func AssessmentKey(path, current, latest string) string {
	return path + "\x00" + current + "\x00" + latest
}

func PRTitle(applied []update.Applied) string {
	total := 0
	for _, a := range applied {
		total += a.Count
	}
	if total == 1 {
		g := applied[0].Group
		return fmt.Sprintf("Bump %s from %s to %s", g.ActionPath, g.Current, g.Latest)
	}
	return "Bump GitHub Actions dependencies"
}

func details(b *strings.Builder, title, source, body string) {
	_, _ = fmt.Fprintf(b, "<details>\n<summary>%s</summary>\n\n_%s_\n\n", title, source)
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if line == "" {
			b.WriteString(">\n")
		} else {
			_, _ = fmt.Fprintf(b, "> %s\n", line)
		}
	}
	b.WriteString("</details>\n\n")
}

func htmlSafe(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || (r >= 127 && r <= 159) {
			return ' '
		}
		return r
	}, s)
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func PRBody(ctx context.Context, client *github.Client, applied []update.Applied, findings []model.Finding, assessments map[string]model.Assessment) string {
	groups := []update.Applied{}
	index := map[string]int{}
	for _, a := range applied {
		key := AssessmentKey(a.Group.ActionPath, a.Group.Current, a.Group.Latest)
		if i, ok := index[key]; ok {
			groups[i].Count += a.Count
		} else {
			index[key] = len(groups)
			groups = append(groups, a)
		}
	}
	var b strings.Builder
	links := make([]string, 0, len(groups))
	for _, a := range groups {
		repo := repoOf(a.Group.ActionPath)
		links = append(links, fmt.Sprintf("[%s](https://github.com/%s)", a.Group.ActionPath, repo))
	}
	word := "updates"
	if len(groups) == 1 {
		word = "update"
	}
	_, _ = fmt.Fprintf(&b, "Bumps the github-actions group with %d %s: %s.\n\n", len(groups), word, strings.Join(links, ", "))
	for _, a := range groups {
		g := a.Group
		repo := repoOf(g.ActionPath)
		base := "https://github.com/" + repo
		_, _ = fmt.Fprintf(&b, "## [%s](%s)\n\n", g.ActionPath, base)
		_, _ = fmt.Fprintf(&b, "Updates `%s` from %s to %s", g.ActionPath, g.Current, g.Latest)
		if a.Count > 1 {
			_, _ = fmt.Fprintf(&b, " across %d workflow entries", a.Count)
		}
		b.WriteString(".\n\n")
		for _, f := range findings {
			if strings.SplitN(f.Action, "@", 2)[0] == g.ActionPath && f.Current == g.Current && f.Latest == g.Latest && f.LatestSHA != nil {
				if f.CurrentTag != nil {
					_, _ = fmt.Fprintf(&b, "Current SHA matches tag: `%s`.\n\n", *f.CurrentTag)
				}
				if f.CommitsSince != nil {
					_, _ = fmt.Fprintf(&b, "Commits since current SHA: %d.\n\n", *f.CommitsSince)
				}
				_, _ = fmt.Fprintf(&b, "Pinned update: `%s@%s`.\n\n", g.ActionPath, f.UpdateRef)
				break
			}
		}
		if assessment, ok := assessments[AssessmentKey(g.ActionPath, g.Current, g.Latest)]; ok {
			investigation(&b, assessment)
		}
		if notes := client.ReleaseNotes(ctx, repo, g.Current, g.Latest); notes != "" {
			details(&b, "Release notes", fmt.Sprintf("Sourced from [%s's releases](%s/releases).", repo, base), notes)
		} else {
			details(&b, "Release notes", fmt.Sprintf("No release notes were found in [%s's releases](%s/releases).", repo, base), "actions-snitch could not find release notes for "+github.VersionRef(g.Latest)+".")
		}
		if body, path, branch := client.Changelog(ctx, repo); body != "" {
			details(&b, "Changelog", fmt.Sprintf("Sourced from [%s's changelog](%s/blob/%s/%s).", repo, base, branch, path), body)
		} else {
			details(&b, "Changelog", "No changelog file was found for "+repo+".", "actions-snitch looked for CHANGELOG.md, changelog.md, CHANGES.md, and HISTORY.md.")
		}
		compareURL := base + "/compare/" + github.VersionRef(g.Current) + "..." + github.VersionRef(g.Latest)
		comparison, err := client.Compare(ctx, repo, github.VersionRef(g.Current), github.VersionRef(g.Latest))
		if err == nil && len(comparison.Commits) > 0 {
			var lines strings.Builder
			for i, c := range comparison.Commits {
				if i >= 10 {
					break
				}
				sha := c.SHA
				if len(sha) > 7 {
					sha = sha[:7]
				}
				_, _ = fmt.Fprintf(&lines, "- [`%s`](%s) %s\n", sha, c.URL, strings.SplitN(c.Commit.Message, "\n", 2)[0])
			}
			_, _ = fmt.Fprintf(&lines, "See full diff in [compare view](%s).", compareURL)
			details(&b, "Commits", fmt.Sprintf("Sourced from [%s's commit history](%s).", repo, compareURL), lines.String())
		} else {
			details(&b, "Commits", "No commit comparison was available for "+repo+".", fmt.Sprintf("See the [compare view](%s).", compareURL))
		}
		_, _ = fmt.Fprintf(&b, "[![Dependabot compatibility score](https://dependabot-badges.githubapp.com/badges/compatibility_score?dependency-name=%s&package-manager=github_actions&previous-version=%s&new-version=%s)](https://docs.github.com/en/github/managing-security-vulnerabilities/about-dependabot-security-updates#about-compatibility-scores)\n\n", repo, g.Current, g.Latest)
	}
	b.WriteString("<sub>Findings and PR created by [actions-snitch](https://github.com/wallentx/actions-snitch).</sub>\n")
	return b.String()
}

func repoOf(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, "/")
}

func investigation(b *strings.Builder, a model.Assessment) {
	b.WriteString("<details>\n<summary>Investigation: Compatibility & Safety Details</summary>\n\n")
	for _, f := range a.Findings {
		_, _ = fmt.Fprintf(b, "* **Caution Detail:** %s\n* **Safety:** %s\n", htmlSafe(f.Detail), htmlSafe(f.Safety))
	}
	for _, r := range a.Remediations {
		if r.Operation == "set" {
			_, _ = fmt.Fprintf(b, "* **Remediation:** Set <code>%s</code> from <code>%s</code> to <code>%s</code> in <code>%s:%d</code>. %s\n", htmlSafe(r.Input), htmlSafe(r.CurrentValue), htmlSafe(r.NewValue), htmlSafe(r.File), r.Line, htmlSafe(r.Reason))
		} else {
			_, _ = fmt.Fprintf(b, "* **Remediation:** Remove <code>%s</code> (currently <code>%s</code>) in <code>%s:%d</code>. %s\n", htmlSafe(r.Input), htmlSafe(r.CurrentValue), htmlSafe(r.File), r.Line, htmlSafe(r.Reason))
		}
	}
	b.WriteString("</details>\n\n")
}
