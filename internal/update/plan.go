// Package update prepares and verifies narrowly scoped YAML mutations.
package update

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/safety"
	"github.com/wallentx/actions-snitch/internal/workflow"
	"gopkg.in/yaml.v3"
)

type Group struct {
	ActionPath   string
	Current      string
	Latest       string
	Target       string
	Remediations []model.Remediation
}

type Change struct {
	File  *workflow.File
	Data  []byte
	Count int
}

type Applied struct {
	Group Group
	File  string
	Count int
}
type Plan struct {
	Changes []Change
	Applied []Applied
}

type prepared struct {
	file       *workflow.File
	actual     []*yaml.Node
	expected   []*yaml.Node
	origins    map[*yaml.Node][]*yaml.Node
	scalars    map[*yaml.Node]string
	remediated bool
	count      int
}

// Build uses only original snapshots. It does not read or write the working tree.
func Build(files []*workflow.File, groups []Group) (*Plan, error) {
	if len(groups) == 0 {
		return &Plan{}, nil
	}
	preparedFiles := map[string]*prepared{}
	for _, f := range files {
		needed := false
		for _, g := range groups {
			for _, u := range f.Usages {
				if u.ActionPath == g.ActionPath && u.Current == g.Current {
					needed = true
				}
			}
			for _, r := range g.Remediations {
				if r.File == f.Path {
					needed = true
				}
			}
		}
		if !needed {
			continue
		}
		p := &prepared{file: f, scalars: map[*yaml.Node]string{}, origins: map[*yaml.Node][]*yaml.Node{}}
		for _, doc := range f.Documents {
			expanded, origins, err := workflow.ExpandedOrigins(doc)
			if err != nil {
				return nil, err
			}
			p.actual = append(p.actual, workflow.Clone(doc))
			p.expected = append(p.expected, expanded)
			for source, copies := range origins {
				p.origins[source] = copies
			}
		}
		preparedFiles[f.Path] = p
	}
	plan := &Plan{}
	remediations := map[string]bool{}
	for _, group := range groups {
		if group.Target == "" || strings.ContainsAny(group.Target, "\r\n\x00") {
			return nil, fmt.Errorf("invalid target ref")
		}
		for _, r := range group.Remediations {
			key := fmt.Sprintf("%s\x00%d\x00%s", r.File, r.Line, r.Input)
			if remediations[key] {
				return nil, fmt.Errorf("duplicate or conflicting remediation")
			}
			remediations[key] = true
			p := preparedFiles[r.File]
			if p == nil {
				return nil, fmt.Errorf("remediation file was not scanned")
			}
			u, err := validateRemediation(p.file, group, r)
			if err != nil {
				return nil, err
			}
			if err := applyInput(workflow.At(p.actual[u.Document], u.Path), r); err != nil {
				return nil, err
			}
			for _, copy := range p.origins[u.Step] {
				if err := applyInput(copy, r); err != nil {
					return nil, err
				}
			}
			p.remediated = true
		}
		for _, f := range files {
			p := preparedFiles[f.Path]
			if p == nil {
				continue
			}
			count := 0
			for _, u := range f.Usages {
				if u.ActionPath != group.ActionPath || u.Current != group.Current {
					continue
				}
				value := group.ActionPath + "@" + group.Target
				original := workflow.Dereference(u.Node)
				if prior, ok := p.scalars[original]; ok && prior != value {
					return nil, fmt.Errorf("conflicting action targets")
				}
				p.scalars[original] = value
				actual := workflow.Dereference(workflow.Field(workflow.At(p.actual[u.Document], u.Path), "uses"))
				if actual == nil {
					return nil, fmt.Errorf("missing action scalar")
				}
				actual.Value = value
				actual.Tag = "!!str"
				for _, copy := range p.origins[u.Step] {
					expected := workflow.Field(copy, "uses")
					if expected == nil {
						return nil, fmt.Errorf("missing expected scalar")
					}
					expected.Value = value
					expected.Tag = "!!str"
				}

				// Alias tokens are preserved and were not counted as writes by yq.
				if u.Node.Kind != yaml.AliasNode {
					count++
				}
			}
			if count > 0 {
				p.count += count
				plan.Applied = append(plan.Applied, Applied{group, f.Path, count})
			}
		}
	}
	paths := make([]string, 0, len(preparedFiles))
	for path := range preparedFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		p := preparedFiles[path]
		if len(p.scalars) == 0 && !p.remediated {
			continue
		}
		var data []byte
		var err error
		if p.remediated {
			var b bytes.Buffer
			encoder := yaml.NewEncoder(&b)
			encoder.SetIndent(2)
			for _, doc := range p.actual {
				if err = encoder.Encode(doc); err != nil {
					break
				}
			}
			closeErr := encoder.Close()
			if err == nil {
				err = closeErr
			}
			data = b.Bytes()
		} else {
			data, err = patchScalars(p.file.Original, p.scalars)
		}
		if err != nil {
			return nil, fmt.Errorf("prepare %s: %w", path, err)
		}
		parsed, err := workflow.Parse(path, data)
		if err != nil {
			return nil, err
		}
		if !equivalent(parsed.Documents, p.expected) {
			return nil, fmt.Errorf("refusing to change unrelated YAML in %s", path)
		}
		if !bytes.Equal(data, p.file.Original) {
			plan.Changes = append(plan.Changes, Change{p.file, bytes.Clone(data), p.count})
		}
	}
	return plan, nil
}

func equivalent(a, b []*yaml.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		var left, right any
		if a[i].Decode(&left) != nil || b[i].Decode(&right) != nil || !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

var inputName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func validateRemediation(f *workflow.File, g Group, r model.Remediation) (workflow.Usage, error) {
	fail := func() (workflow.Usage, error) {
		return workflow.Usage{}, fmt.Errorf("unsafe remediation for %s:%d input %s", r.File, r.Line, r.Input)
	}
	if r.File == "" || strings.HasPrefix(r.File, "/") || strings.ContainsAny(r.File, "\r\n\x00") || !inputName.MatchString(r.Input) || r.Line < 1 {
		return fail()
	}
	for _, part := range strings.Split(r.File, "/") {
		if part == ".." || part == "." || part == "" {
			return fail()
		}
	}
	if strings.ContainsAny(r.CurrentValue+r.NewValue, "\r\n\x00") || safety.SensitiveName(r.Input) || safety.SecretReference(r.CurrentValue) || safety.SecretReference(r.NewValue) || r.CurrentValue == "<redacted>" {
		return fail()
	}
	if r.Operation != "set" && r.Operation != "remove" {
		return fail()
	}
	if r.Operation == "set" && r.NewValue == "<removed>" {
		return fail()
	}
	if r.Operation == "remove" && (r.NewValue != "<removed>" || r.CurrentValue == "<absent>") {
		return fail()
	}
	for _, doc := range f.Documents {
		if hasMarker(doc) {
			return fail()
		}
	}
	var matches []workflow.Usage
	for _, u := range f.Usages {
		if u.Line == r.Line {
			matches = append(matches, u)
		}
	}
	if len(matches) != 1 {
		return fail()
	}
	u := matches[0]
	if u.ActionPath != g.ActionPath || u.Current != g.Current || u.Node.Kind == yaml.AliasNode {
		return fail()
	}
	with := workflow.Field(u.Step, "with")
	if with != nil && (with.Kind != yaml.MappingNode || with.Alias != nil) {
		return fail()
	}
	value := workflow.Field(with, r.Input)
	actual := "<absent>"
	if value != nil {
		if value.Kind != yaml.ScalarNode {
			return fail()
		}
		switch value.Tag {
		case "!!str", "!!int", "!!float", "!!bool":
			actual = value.Value
		case "!!null":
			actual = "null"
		default:
			return fail()
		}
	}
	if actual != r.CurrentValue {
		return fail()
	}
	return u, nil
}

func hasMarker(n *yaml.Node) bool {
	if workflow.Field(n, "x-actions-snitch-remediation") != nil {
		return true
	}
	for _, child := range n.Content {
		if hasMarker(child) {
			return true
		}
	}
	return false
}

func applyInput(step *yaml.Node, r model.Remediation) error {
	if step == nil || step.Kind != yaml.MappingNode {
		return fmt.Errorf("remediation target is not a mapping")
	}
	with := workflow.Field(step, "with")
	if with == nil {
		with = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		step.Content = append(step.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "with"}, with)
	}
	if with.Kind != yaml.MappingNode {
		return fmt.Errorf("with inputs are not a mapping")
	}
	for i := 0; i < len(with.Content); i += 2 {
		if with.Content[i].Value != r.Input {
			continue
		}
		if r.Operation == "remove" {
			with.Content = append(with.Content[:i], with.Content[i+2:]...)
		} else {
			old := with.Content[i+1]
			replacement := *old
			replacement.Kind = yaml.ScalarNode
			replacement.Tag = "!!str"
			replacement.Value = r.NewValue
			replacement.Content = nil
			replacement.Alias = nil
			with.Content[i+1] = &replacement
		}
		return nil
	}
	if r.Operation == "remove" {
		return fmt.Errorf("cannot remove absent input")
	}
	with.Content = append(with.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: r.Input}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: r.NewValue})
	return nil
}
