// Package workflow discovers action files and retains immutable YAML snapshots.
package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type File struct {
	Path      string
	Original  []byte
	Mode      fs.FileMode
	Info      fs.FileInfo
	Documents []*yaml.Node
	Usages    []Usage
	Problem   error
}

// Usage identifies an original YAML mapping; positions never refer to rewritten bytes.
type Usage struct {
	File       string
	Line       int
	Column     int
	Document   int
	Path       []int
	Action     string
	ActionPath string
	Repository string
	Current    string
	Ref        string
	Node       *yaml.Node
	Step       *yaml.Node
}

func Discover(root string) ([]string, error) {
	paths, _, err := DiscoverWithWarnings(root)
	return paths, err
}

func DiscoverWithWarnings(root string) ([]string, []error, error) {
	var warnings []error
	var ignore Ignore
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, warnings, err
	}
	defer func() { _ = directory.Close() }()
	if b, err := directory.ReadFile(".snitchignore"); err == nil {
		ignore = ParseIgnore(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, warnings, err
	}
	paths := map[string]bool{}
	workflows := filepath.Join(root, ".github", "workflows")
	if info, err := os.Lstat(workflows); err == nil && info.IsDir() {
		if err := filepath.WalkDir(workflows, func(p string, entry fs.DirEntry, err error) error {
			if err != nil {
				warnings = append(warnings, err)
				return nil
			}
			if entry.Type().IsRegular() && (strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml")) {
				rel, e := filepath.Rel(root, p)
				if e != nil {
					return e
				}
				paths[filepath.ToSlash(rel)] = true
			}
			return nil
		}); err != nil {
			return nil, warnings, err
		}
	}
	if err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			warnings = append(warnings, err)
			return nil
		}
		if entry.IsDir() && p != root && (entry.Name() == ".git" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if entry.Type().IsRegular() && (entry.Name() == "action.yml" || entry.Name() == "action.yaml") {
			rel, e := filepath.Rel(root, p)
			if e != nil {
				return e
			}
			paths[filepath.ToSlash(rel)] = true
		}
		return nil
	}); err != nil {
		return nil, warnings, err
	}
	result := make([]string, 0, len(paths))
	for p := range paths {
		if !ignore.Matches(p) {
			result = append(result, p)
		}
	}
	sort.Strings(result)
	return result, warnings, nil
}

func Read(root, path string) (*File, error) {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	info, err := directory.Lstat(filepath.FromSlash(path))
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	b, err := directory.ReadFile(filepath.FromSlash(path))
	if err != nil {
		return nil, err
	}
	f, err := Parse(path, b)
	if err != nil {
		return nil, err
	}
	f.Mode, f.Info = info.Mode(), info
	return f, nil
}

func Parse(path string, b []byte) (*File, error) {
	f := &File{Path: path, Original: bytes.Clone(b)}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		// Decoding validates duplicate keys and recursive aliases before traversal.
		var check any
		if err := doc.Decode(&check); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		f.Documents = append(f.Documents, &doc)
		walk(&doc, nil, func(node *yaml.Node, nodePath []int) {
			if node.Kind != yaml.MappingNode {
				return
			}
			uses := Field(node, "uses")
			if uses == nil {
				return
			}
			value := Dereference(uses)
			if value == nil || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return
			}
			parts := strings.SplitN(value.Value, "@", 2)
			if len(parts) != 2 {
				return
			}
			actionPath, ref := parts[0], parts[1]
			components := strings.Split(actionPath, "/")
			repo := actionPath
			if len(components) >= 2 {
				repo = strings.Join(components[:2], "/")
			}
			f.Usages = append(f.Usages, Usage{File: path, Line: uses.Line, Column: uses.Column, Document: len(f.Documents) - 1, Path: append([]int(nil), nodePath...), Action: value.Value, ActionPath: actionPath, Repository: repo, Current: strings.TrimPrefix(ref, "v"), Ref: ref, Node: uses, Step: node})
		})
	}
	return f, nil
}

func (u Usage) Scannable() bool {
	return !strings.Contains(u.ActionPath, "docker://") && strings.Count(u.ActionPath, "/") < 3 && u.Current != "main" && u.Current != "master"
}

func walk(n *yaml.Node, path []int, visit func(*yaml.Node, []int)) {
	visit(n, path)
	// Alias targets have their own source owner and must not be traversed twice.
	for i, child := range n.Content {
		walk(child, append(append([]int(nil), path...), i), visit)
	}
}

func Field(n *yaml.Node, name string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == name {
			return n.Content[i+1]
		}
	}
	return nil
}

func Dereference(n *yaml.Node) *yaml.Node {
	seen := map[*yaml.Node]bool{}
	for n != nil && n.Kind == yaml.AliasNode {
		if seen[n] {
			return nil
		}
		seen[n] = true
		n = n.Alias
	}
	return n
}

func At(n *yaml.Node, path []int) *yaml.Node {
	for _, i := range path {
		if i < 0 || i >= len(n.Content) {
			return nil
		}
		n = n.Content[i]
	}
	return n
}

// Clone preserves alias ownership within a copied document.
func Clone(n *yaml.Node) *yaml.Node {
	copies := map[*yaml.Node]*yaml.Node{}
	var clone func(*yaml.Node) *yaml.Node
	clone = func(old *yaml.Node) *yaml.Node {
		if old == nil {
			return nil
		}
		if existing := copies[old]; existing != nil {
			return existing
		}
		c := *old
		copies[old] = &c
		c.Content = make([]*yaml.Node, len(old.Content))
		for i, child := range old.Content {
			c.Content[i] = clone(child)
		}
		c.Alias = clone(old.Alias)
		return &c
	}
	return clone(n)
}

// Expanded produces independent alias values for mutation-scope comparison.
func Expanded(n *yaml.Node) (*yaml.Node, error) {
	expanded, _, err := ExpandedOrigins(n)
	return expanded, err
}

// ExpandedOrigins retains source ownership for whole-mapping aliases.
func ExpandedOrigins(n *yaml.Node) (*yaml.Node, map[*yaml.Node][]*yaml.Node, error) {
	origins := map[*yaml.Node][]*yaml.Node{}
	active := map[*yaml.Node]bool{}
	count := 0
	var expand func(*yaml.Node) (*yaml.Node, error)
	expand = func(n *yaml.Node) (*yaml.Node, error) {
		count++
		if count > 1000000 || active[n] {
			return nil, errors.New("excessive or recursive YAML aliases")
		}
		active[n] = true
		defer delete(active, n)
		if n.Kind == yaml.AliasNode {
			if n.Alias == nil {
				return nil, errors.New("invalid YAML alias")
			}
			return expand(n.Alias)
		}
		c := *n
		c.Anchor = ""
		c.Alias = nil
		c.Content = make([]*yaml.Node, len(n.Content))
		for i, child := range n.Content {
			v, err := expand(child)
			if err != nil {
				return nil, err
			}
			c.Content[i] = v
		}
		origins[n] = append(origins[n], &c)
		return &c, nil
	}
	expanded, err := expand(n)
	return expanded, origins, err
}
