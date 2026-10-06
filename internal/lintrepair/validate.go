package lintrepair

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/wallentx/actions-snitch/internal/workflow"
)

var diagnosticLine = regexp.MustCompile(`^[0-9]+:[0-9]+:[a-z-]+$`)
var parserLine = regexp.MustCompile(`line ([0-9]+)`)

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxBytes {
		return 0, errors.New("diagnostics exceed size limit")
	}
	return b.Buffer.Write(p)
}

// Validate never returns source-bearing parser or linter messages. External shell
// linters are disabled; repository workflow commands are never executed.
func Validate(ctx context.Context, root, path string, data []byte) (string, error) {
	if len(data) > MaxBytes {
		return "", errors.New("workflow exceeds lint size limit")
	}
	parsed, parseErr := workflow.Parse(path, data)
	parserDiagnostic := ""
	if parseErr != nil {
		parserDiagnostic = "YAML syntax error"
		if line := parserLine.FindStringSubmatch(parseErr.Error()); len(line) > 1 {
			parserDiagnostic += " at line " + line[1]
		}
	} else if len(parsed.Documents) != 1 {
		parserDiagnostic = "A workflow must have exactly one YAML document"
	}
	// #nosec G204 -- The executable and flags are fixed; the path is a filename option value, never shell code.
	cmd := exec.CommandContext(ctx, "actionlint", "-no-color", "-shellcheck=", "-pyflakes=", "-format", `{{range .}}{{.Line}}:{{.Column}}:{{.Kind}}
{{end}}`, "-stdin-filename", path, "-")
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(data)
	var output, stderr boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return "", errors.New("actionlint could not validate the workflow; ensure actionlint is installed")
		}
	}
	lines := []string{}
	if parserDiagnostic != "" {
		lines = append(lines, parserDiagnostic)
	}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		if !diagnosticLine.MatchString(line) {
			return "", errors.New("actionlint returned unsupported diagnostic output")
		}
		lines = append(lines, line)
	}
	if err != nil && len(lines) == 0 {
		return "", errors.New("actionlint failed without structured diagnostics")
	}
	return strings.Join(lines, "\n"), nil
}

func Check(ctx context.Context, root, path string, data []byte) error {
	diagnostics, err := Validate(ctx, root, path, data)
	if err != nil {
		return err
	}
	if diagnostics != "" {
		return fmt.Errorf("lint proposal still fails validation: %s", diagnostics)
	}
	return nil
}
