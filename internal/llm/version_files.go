package llm

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wallentx/actions-snitch/internal/safety"
	"github.com/wallentx/actions-snitch/internal/workflow"
	"gopkg.in/yaml.v3"
)

type versionFileEvidence struct {
	Path      string `json:"path"`
	Available bool   `json:"available"`
	Go        string `json:"go,omitempty"`
	Toolchain string `json:"toolchain,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

var goDirective = regexp.MustCompile(`(?m)^go[ \t]+([0-9]+\.[0-9]+(?:\.[0-9]+)?)[ \t]*(?://[^\n]*)?$`)
var toolchainDirective = regexp.MustCompile(`(?m)^toolchain[ \t]+(go[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:rc[0-9]+|beta[0-9]+)?|default)[ \t]*(?://[^\n]*)?$`)
var anyGoDirective = regexp.MustCompile(`(?m)^go(?:[ \t]|$)`)
var anyToolchainDirective = regexp.MustCompile(`(?m)^toolchain(?:[ \t]|$)`)

// Version-file evidence exports only validated version directives. Module paths,
// dependencies, comments, credentials, and arbitrary file contents stay local.
func localVersionFiles(root string, files []*workflow.File, actionPath, current string) []versionFileEvidence {
	var result []versionFileEvidence
	seen := map[string]bool{}
	for _, file := range files {
		for _, usage := range file.Usages {
			if usage.ActionPath != actionPath || usage.Current != current {
				continue
			}
			with := workflow.Dereference(workflow.Field(usage.Step, "with"))
			input := workflow.Dereference(workflow.Field(with, "go-version-file"))
			if input == nil || input.Kind != yaml.ScalarNode || input.Tag != "!!str" || seen[input.Value] {
				continue
			}
			seen[input.Value] = true
			result = append(result, readVersionFile(root, input.Value))
		}
	}
	return result
}

func readVersionFile(root, path string) versionFileEvidence {
	result := versionFileEvidence{Path: "<unavailable>", Problem: "version file is unavailable or unsupported"}
	if !filepath.IsLocal(path) || strings.Contains(path, "${{") || safety.SecretReference(path) {
		return result
	}
	if base := filepath.Base(path); base != "go.mod" && base != "go.work" {
		return result
	}
	result.Path = path
	directory, err := os.OpenRoot(root)
	if err != nil {
		return result
	}
	defer func() { _ = directory.Close() }()
	info, err := directory.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return result
	}
	f, err := directory.Open(path)
	if err != nil {
		return result
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return result
	}
	source := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.Contains(source, "/*") || strings.Contains(source, "*/") {
		result.Problem = "version directives in files with block comments require manual review"
		return result
	}
	goMatches := goDirective.FindAllStringSubmatch(source, -1)
	toolchainMatches := toolchainDirective.FindAllStringSubmatch(source, -1)
	if len(goMatches) != 1 || len(toolchainMatches) > 1 || len(anyGoDirective.FindAllString(source, -1)) != len(goMatches) || len(anyToolchainDirective.FindAllString(source, -1)) != len(toolchainMatches) {
		result.Problem = "version directives could not be resolved unambiguously"
		return result
	}
	result.Go = goMatches[0][1]
	if len(toolchainMatches) == 1 {
		result.Toolchain = toolchainMatches[0][1]
	}
	result.Available, result.Problem = true, ""
	return result
}
