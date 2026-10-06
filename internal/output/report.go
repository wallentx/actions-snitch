// Package output renders deterministic findings without performing mutations.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/wallentx/actions-snitch/internal/model"
)

func text(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func number(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}
func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
func optional(s *string) string {
	if s == nil {
		return "null"
	}
	return quote(*s)
}
func escape(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ") }
func Label(s string, verified bool) string {
	if verified {
		return "☑️ " + s
	}
	return s
}

var yamlKey = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Report writes nothing when there are no findings, preserving the CLI contract.
func Report(w io.Writer, format, repo, repoURL string, findings []model.Finding) error {
	if len(findings) == 0 {
		return nil
	}
	var b bytes.Buffer
	switch format {
	case "json":
		encoder := json.NewEncoder(&b)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(map[string][]model.Finding{repo: findings}); err != nil {
			return err
		}
	case "yaml":
		key := repo
		if !yamlKey.MatchString(key) {
			key = quote(key)
		}
		_, _ = fmt.Fprintf(&b, "%s:\n", key)
		for _, f := range findings {
			count := "null"
			if f.CommitsSince != nil {
				count = number(f.CommitsSince)
			}
			score := quote(fmt.Sprint(f.CompatibilityScore))
			if n, ok := f.CompatibilityScore.(int); ok {
				score = strconv.Itoa(n)
			}
			_, _ = fmt.Fprintf(&b, "  - file: %s\n    line: %d\n    action: %s\n    repository: %s\n    current: %s\n    latest: %s\n    current_tag: %s\n    latest_sha: %s\n    commits_since: %s\n    update_ref: %s\n    compatibility_score: %s\n    verified_creator: %t\n    release_notes: %s\n", quote(f.File), f.Line, quote(f.Action), quote(f.Repository), quote(f.Current), quote(f.Latest), optional(f.CurrentTag), optional(f.LatestSHA), count, quote(f.UpdateRef), score, f.VerifiedCreator, optional(f.ReleaseNotes))
		}
	case "md":
		if repoURL != "" {
			label := strings.NewReplacer(`\`, `\\`, "]", `\]`, "\n", " ").Replace(repo)
			_, _ = fmt.Fprintf(&b, "## [%s](%s)\n\n", label, repoURL)
		} else {
			_, _ = fmt.Fprintf(&b, "## %s\n\n", repo)
		}
		b.WriteString("| File | Line | Action | Current | Latest | Compatibility | Release Notes | Current SHA tag | Latest SHA | Commits since | Update ref |\n| --- | ---: | --- | --- | --- | ---: | --- | --- | --- | ---: | --- |\n")
		for _, f := range findings {
			score := fmt.Sprint(f.CompatibilityScore)
			if _, ok := f.CompatibilityScore.(int); ok {
				score += "%"
			}
			values := []string{f.File, strconv.Itoa(f.Line), Label(f.Action, f.VerifiedCreator), f.Current, f.Latest, score, text(f.ReleaseNotes), text(f.CurrentTag), text(f.LatestSHA), number(f.CommitsSince), f.UpdateRef}
			for i, v := range values {
				values[i] = escape(v)
			}
			_, _ = fmt.Fprintf(&b, "| %s |\n", strings.Join(values, " | "))
		}
	default:
		return fmt.Errorf("unknown report format %q", format)
	}
	_, err := w.Write(b.Bytes())
	return err
}

func Human(w io.Writer, findings []model.Finding, pin bool) error {
	return HumanStyled(w, findings, pin, Style{})
}

func HumanStyled(w io.Writer, findings []model.Finding, pin bool, style Style) error {
	var b bytes.Buffer
	lastFile := ""
	for _, f := range findings {
		if lastFile != f.File {
			_, _ = fmt.Fprintln(&b, style.Wrap("section", "Findings in "+f.File+":"))
			lastFile = f.File
		}
		humanFinding(&b, f, pin, style, "      Line: "+style.Wrap("info", strconv.Itoa(f.Line))+"\n")
	}
	_, err := w.Write(b.Bytes())
	return err
}

// HumanGroupStyled renders the common metadata once and retains every location.
// Callers group findings by the same action path and current/proposed versions.
func HumanGroupStyled(w io.Writer, findings []model.Finding, pin bool, style Style) error {
	if len(findings) < 2 {
		return HumanStyled(w, findings, pin, style)
	}
	var files []string
	lines := map[string][]string{}
	for _, f := range findings {
		if _, ok := lines[f.File]; !ok {
			files = append(files, f.File)
		}
		lines[f.File] = append(lines[f.File], strconv.Itoa(f.Line))
	}
	var locations strings.Builder
	locations.WriteString("      Locations:\n")
	for _, file := range files {
		_, _ = fmt.Fprintf(&locations, "        %s: %s\n", style.Wrap("code", file), style.Wrap("info", strings.Join(lines[file], ", ")))
	}
	var b bytes.Buffer
	header := fmt.Sprintf("Findings in %d files (%d references):", len(files), len(findings))
	if len(files) == 1 {
		header = fmt.Sprintf("Findings in %s (%d references):", files[0], len(findings))
	}
	_, _ = fmt.Fprintln(&b, style.Wrap("section", header))
	humanFinding(&b, findings[0], pin, style, locations.String())
	_, err := w.Write(b.Bytes())
	return err
}

func humanFinding(b *bytes.Buffer, f model.Finding, pin bool, style Style, locations string) {
	kind := "is outdated"
	if pin && f.Current == f.Latest {
		kind = "can be pinned to a commit SHA"
	}
	_, _ = fmt.Fprintf(b, "    ❗ %s\n%s      Current: %s\n", style.Wrap("warning", Label(f.Repository, f.VerifiedCreator)+" "+kind+":"), locations, style.Wrap("warning", f.Current))
	if f.CurrentTag != nil {
		_, _ = fmt.Fprintf(b, "      Current SHA matches tag: %s\n", style.Wrap("info", *f.CurrentTag))
	} else if f.CommitsSince != nil {
		_, _ = fmt.Fprintf(b, "      Commits since: %s\n", style.Wrap("info", strconv.Itoa(*f.CommitsSince)))
	}
	_, _ = fmt.Fprintf(b, "      Latest: %s\n", style.Wrap("success", f.Latest))
	if f.LatestSHA != nil {
		_, _ = fmt.Fprintf(b, "      Latest SHA: %s\n", style.Wrap("success", *f.LatestSHA))
	}
	color := "\x1b[48;2;153;153;153m"
	score := "unknown"
	if n, ok := f.CompatibilityScore.(int); ok {
		score = strconv.Itoa(n) + "%"
		color = "\x1b[48;2;51;204;17m"
		if n < 80 {
			color = "\x1b[48;2;255;19;51m"
		}
	}
	_, _ = fmt.Fprintf(b, "      \x1b[48;2;85;85;85m🤖compatibility:%s %s \x1b[0m\n", color, score)
	if f.ReleaseNotes != nil {
		_, _ = fmt.Fprintf(b, "      Release Notes: %s\n", style.Wrap("code", *f.ReleaseNotes))
	}
	b.WriteByte('\n')
}
