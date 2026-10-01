package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/wallentx/actions-snitch/internal/github"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/safety"
	"github.com/wallentx/actions-snitch/internal/workflow"
	"gopkg.in/yaml.v3"
)

// Sanitize applies the same rules independently to every nested env and with map.
func Sanitize(value any) any {
	switch v := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, item := range v {
			if mapping, ok := item.(map[string]any); ok && (key == "env" || key == "with") {
				clean := make(map[string]any, len(mapping))
				for name, input := range mapping {
					if key == "env" || safety.SensitiveName(name) {
						clean[name] = "<redacted>"
					} else {
						clean[name] = Sanitize(input)
					}
				}
				result[key] = clean
			} else {
				result[key] = Sanitize(item)
			}
		}
		return result
	case []any:
		r := make([]any, len(v))
		for i, item := range v {
			r[i] = Sanitize(item)
		}
		return r
	case string:
		if safety.SecretReference(v) {
			return "<redacted>"
		}
		return v
	default:
		return value
	}
}

type UsageEvidence struct {
	File     string `json:"file"`
	Lines    []int  `json:"action_lines"`
	Document any    `json:"document"`
}
type ImplementationFile struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Available bool   `json:"available"`
	Complete  bool   `json:"complete"`
}
type Implementation struct {
	Files   []ImplementationFile `json:"files"`
	Omitted int                  `json:"files_omitted"`
}

var implementationPath = regexp.MustCompile(`github[.]action_path\s*[}][}]/[[:alnum:]_./-]+`)

func implementation(ctx context.Context, client *github.Client, repo, ref, actionPath, definition string) Implementation {
	result := Implementation{Files: []ImplementationFile{}}
	var action struct {
		Runs struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if yaml.Unmarshal([]byte(definition), &action) != nil {
		return result
	}
	paths := map[string]bool{}
	for _, step := range action.Runs.Steps {
		for _, match := range implementationPath.FindAllString(step.Run, -1) {
			path := strings.SplitN(match, "}}/", 2)[1]
			safe := !strings.HasPrefix(path, "/")
			for _, part := range strings.Split(path, "/") {
				if part == ".." {
					safe = false
				}
			}
			if safe {
				paths[path] = true
			}
		}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	if len(sorted) > 4 {
		result.Omitted = len(sorted) - 4
		sorted = sorted[:4]
	}
	prefix := strings.TrimPrefix(strings.TrimPrefix(actionPath, repo), "/")
	if prefix != "" {
		prefix += "/"
	}
	for _, path := range sorted {
		path = prefix + path
		body, err := client.Raw(ctx, repo, ref, path)
		result.Files = append(result.Files, ImplementationFile{Path: path, Body: github.LimitCharacters(string(body), 12000), Available: err == nil, Complete: err == nil && len([]rune(string(body))) <= 12000})
	}
	return result
}

func Evidence(ctx context.Context, client *github.Client, files []*workflow.File, f model.Finding, issuePolicy string) ([]byte, error) {
	actionPath := strings.SplitN(f.Action, "@", 2)[0]
	var usages []UsageEvidence
	for _, file := range files {
		if file.Problem != nil {
			return nil, fmt.Errorf("local workflow evidence is unreadable: %s", file.Path)
		}
		for docIndex, doc := range file.Documents {
			var lines []int
			for _, u := range file.Usages {
				if u.Document == docIndex && u.ActionPath == actionPath && u.Current == f.Current && u.Node.Kind != yaml.AliasNode {
					lines = append(lines, u.Line)
				}
			}
			if len(lines) == 0 {
				continue
			}
			var value any
			if err := doc.Decode(&value); err != nil {
				return nil, err
			}
			usages = append(usages, UsageEvidence{file.Path, lines, Sanitize(value)})
		}
	}
	if len(usages) == 0 {
		return nil, fmt.Errorf("missing local action usage")
	}
	currentRef, proposedRef := github.VersionRef(f.Current), github.VersionRef(f.Latest)
	current, err := client.Definition(ctx, f.Repository, currentRef, actionPath)
	if err != nil {
		return nil, err
	}
	proposed, err := client.Definition(ctx, f.Repository, proposedRef, actionPath)
	if err != nil {
		return nil, err
	}
	releases, _ := client.Releases(ctx, f.Repository)
	comparison, _ := client.Compare(ctx, f.Repository, currentRef, proposedRef)
	if len(comparison.Commits) > 10 {
		comparison.Commits = comparison.Commits[:10]
	}
	changelog, _, _ := client.Changelog(ctx, f.Repository)
	var issues []github.Issue
	if issuePolicy != "never" {
		issues, _ = client.Issues(ctx, f.Repository, f.Latest)
	}
	evidence := map[string]any{
		"upgrade":      map[string]any{"repository": f.Repository, "action_path": actionPath, "current": f.Current, "proposed": f.Latest, "compatibility_score": fmt.Sprint(f.CompatibilityScore)},
		"local_usages": usages,
		"upstream":     map[string]any{"releases": github.LimitCharacters(github.JSONText(releases), 16000), "changelog": github.LimitCharacters(changelog, 16000), "commits": github.LimitCharacters(github.JSONText(comparison.Commits), 8000), "issues": github.LimitCharacters(github.JSONText(issues), 10000), "current_action_definition": current, "proposed_action_definition": proposed, "current_action_implementation": implementation(ctx, client, f.Repository, currentRef, actionPath, current), "proposed_action_implementation": implementation(ctx, client, f.Repository, proposedRef, actionPath, proposed)},
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(evidence)
}
