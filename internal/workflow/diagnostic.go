package workflow

import (
	"regexp"
	"strings"
)

var diagnosticLine = regexp.MustCompile(`^yaml: (?:unmarshal errors:\n[ \t]+)?line ([0-9]+):`)

// YAMLDiagnostic preserves actionable locations and known parser categories
// without echoing scalar values or mapping keys from the source document.
func YAMLDiagnostic(err error) string {
	message := err.Error()
	prefix := "yaml: "
	if line := diagnosticLine.FindStringSubmatch(message); len(line) > 1 {
		prefix += "line " + line[1] + ": "
	}
	for _, category := range []string{
		"did not find expected key", "did not find expected '-' indicator",
		"mapping values are not allowed in this context", "could not find expected ':'",
		"found character that cannot start any token", "found unexpected end of stream",
		"did not find expected node content", "did not find expected ',' or ']'",
		"did not find expected ',' or '}'", "found invalid UTF-8 octet",
	} {
		if strings.Contains(message, category) {
			return prefix + category
		}
	}
	if strings.Contains(message, "already defined") {
		return prefix + "duplicate mapping key"
	}
	if strings.Contains(message, "cannot decode") || strings.Contains(message, "cannot unmarshal") {
		return prefix + "invalid scalar value or type"
	}
	return prefix + "invalid YAML syntax or value"
}
