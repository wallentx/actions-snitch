package app

import (
	"encoding/json"
	"fmt"
	"strings"

	gitops "github.com/wallentx/actions-snitch/internal/git"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/output"
	"github.com/wallentx/actions-snitch/internal/update"
	"github.com/wallentx/actions-snitch/internal/workflow"
	"gopkg.in/yaml.v3"
)

// prUpdates derives the description from the complete PR comparison. Current-run
// metadata enriches matching changes, but cannot hide already-committed updates.
func prUpdates(changes []gitops.FileChange, recent []update.Applied) ([]update.Applied, error) {
	var result []update.Applied
	positions := map[string]int{}
	for _, change := range changes {
		before, err := workflow.Parse(change.Path, change.Before)
		if err != nil {
			return nil, err
		}
		after, err := workflow.Parse(change.Path, change.After)
		if err != nil {
			return nil, err
		}
		priorUsages, err := matchPRUsages(before, after)
		if err != nil {
			return nil, err
		}
		for i, usage := range after.Usages {
			prior := priorUsages[i]
			if (prior != nil && prior.Ref == usage.Ref) || strings.HasPrefix(usage.ActionPath, "./") || strings.HasPrefix(usage.ActionPath, "docker://") {
				continue
			}
			group := update.Group{ActionPath: usage.ActionPath, Latest: usage.Current, Target: usage.Ref}
			if prior != nil {
				group.Current = prior.Current
			}
			for _, applied := range recent {
				g := applied.Group
				if g.ActionPath == group.ActionPath && g.Target == group.Target {
					group.Latest = g.Latest
					break
				}
			}
			key := change.Path + "\x00" + group.ActionPath + "\x00" + group.Current + "\x00" + group.Latest
			if index, exists := positions[key]; exists {
				result[index].Count++
			} else {
				positions[key] = len(result)
				result = append(result, update.Applied{Group: group, File: change.Path, Count: 1})
			}
		}
	}
	if len(result) == 0 && len(recent) != 0 {
		return nil, fmt.Errorf("the PR comparison contains no identifiable action updates")
	}
	return result, nil
}

// Scope keys retain job/composite boundaries without treating an inserted step
// as a different location for every following action.
func usageScope(file *workflow.File, usage workflow.Usage) string {
	parts := []any{usage.Document}
	node := file.Documents[usage.Document]
	for _, index := range usage.Path {
		if node.Kind == yaml.MappingNode && index%2 == 1 {
			parts = append(parts, node.Content[index-1].Value)
		} else if node.Kind != yaml.SequenceNode {
			parts = append(parts, index)
		}
		node = node.Content[index]
	}
	encoded, _ := json.Marshal(parts)
	return string(encoded) + "\x00" + usage.ActionPath
}

func matchPRUsages(before, after *workflow.File) ([]*workflow.Usage, error) {
	matched := make([]*workflow.Usage, len(after.Usages))
	oldGroups, newGroups := map[string][]int{}, map[string][]int{}
	var scopes []string
	for i, usage := range before.Usages {
		key := usageScope(before, usage)
		oldGroups[key] = append(oldGroups[key], i)
	}
	for i, usage := range after.Usages {
		key := usageScope(after, usage)
		if _, ok := newGroups[key]; !ok {
			scopes = append(scopes, key)
		}
		newGroups[key] = append(newGroups[key], i)
	}
	for _, key := range scopes {
		old, next := oldGroups[key], newGroups[key]
		usedOld, usedNext := map[int]bool{}, map[int]bool{}
		pair := func(a, b int) { matched[b] = &before.Usages[a]; usedOld[a], usedNext[b] = true, true }
		for _, field := range []string{"id", "name"} {
			oldLabels, newLabels := map[string][]int{}, map[string][]int{}
			for _, i := range old {
				if label := usageLabel(before.Usages[i], field); label != "" && !usedOld[i] {
					oldLabels[label] = append(oldLabels[label], i)
				}
			}
			for _, i := range next {
				if label := usageLabel(after.Usages[i], field); label != "" && !usedNext[i] {
					newLabels[label] = append(newLabels[label], i)
				}
			}
			for label, indexes := range oldLabels {
				if candidates := newLabels[label]; len(indexes) == 1 && len(candidates) == 1 {
					pair(indexes[0], candidates[0])
				}
			}
		}
		remaining := func(indexes []int, used map[int]bool) []int {
			var result []int
			for _, i := range indexes {
				if !used[i] {
					result = append(result, i)
				}
			}
			return result
		}
		old, next = remaining(old, usedOld), remaining(next, usedNext)
		if len(old) != len(next) {
			// Added/deleted uses must not turn an unchanged reference into a bump.
			for _, a := range old {
				for _, b := range next {
					if !usedNext[b] && before.Usages[a].Ref == after.Usages[b].Ref {
						pair(a, b)
						break
					}
				}
			}
			old, next = remaining(old, usedOld), remaining(next, usedNext)
			oldRefs, newRefs := map[string]bool{}, map[string]bool{}
			for _, i := range old {
				oldRefs[before.Usages[i].Ref] = true
			}
			for _, i := range next {
				newRefs[after.Usages[i].Ref] = true
			}
			if len(oldRefs) > 1 && len(newRefs) > 1 {
				return nil, fmt.Errorf("ambiguous action-version mapping in %s", after.Path)
			}
		}
		for i := 0; i < min(len(old), len(next)); i++ {
			pair(old[i], next[i])
		}
	}
	return matched, nil
}

func usageLabel(usage workflow.Usage, field string) string {
	node := workflow.Dereference(workflow.Field(usage.Step, field))
	if node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		return node.Value
	}
	return ""
}

func prFindings(findings []model.Finding, applied []update.Applied) []model.Finding {
	// Retain existing resolution metadata only where it describes the same net
	// update. Earlier branch changes have no invented AI or SHA evidence.
	var matching []model.Finding
	for _, f := range findings {
		for _, a := range applied {
			if strings.SplitN(f.Action, "@", 2)[0] == a.Group.ActionPath && f.Current == a.Group.Current && f.Latest == a.Group.Latest {
				matching = append(matching, f)
				break
			}
		}
	}
	return matching
}

// Attempted approvals whose repairs failed validation are not applied evidence.
func appliedAssessments(assessments map[string]model.Assessment, applied []update.Applied) map[string]model.Assessment {
	result := map[string]model.Assessment{}
	for _, a := range applied {
		key := output.AssessmentKey(a.Group.ActionPath, a.Group.Current, a.Group.Latest)
		if assessment, ok := assessments[key]; ok && assessment.Decision == "allow" {
			result[key] = assessment
		}
	}
	return result
}
