package lintrepair

import (
	"strings"
	"testing"
)

func TestPartialSelection(t *testing.T) {
	original := []byte("jobs:\n  build:\n    runs-on: [self-hosted\n    steps:\n      - run: echo private\n\n")
	proposed := []byte("jobs:\n  build:\n    runs-on: [self-hosted]\n    steps:\n      - run: echo private\n")
	partial := []byte("jobs:\n  build:\n    runs-on: [self-hosted]\n    steps:\n      - run: echo private\n\n")
	if err := CheckSelection(original, proposed, partial); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"jobs:\n  build:\n    runs-on: [ubuntu-latest]\n    steps:\n      - run: echo private\n", "jobs:\n  build:\n    runs-on: [self-hosted]\n    steps:\n      - run: echo changed\n"} {
		if err := CheckSelection(original, proposed, []byte(bad)); err == nil {
			t.Fatal("arbitrary edit accepted")
		}
	}
}

func TestPartialSelectionPreservesExplicitBlockIndentation(t *testing.T) {
	for _, header := range []string{"|2-", "|-2", ">2+", ">+2", "!!str &script |2-"} {
		original := "runs-on: [self-hosted\nrun: " + header + "\n    if enabled:\n      sensitive_call()\nnext: unchanged\n\n"
		proposed := strings.ReplaceAll(original, "[self-hosted", "[self-hosted]")
		proposed = strings.Replace(proposed, "      sensitive_call()", "    sensitive_call()", 1)
		proposed = strings.TrimSuffix(proposed, "\n")
		partial := proposed + "\n"
		err := CheckSelection([]byte(original), []byte(proposed), []byte(partial))
		if err == nil || !strings.Contains(err.Error(), "literal block indentation") {
			t.Fatalf("header %s: expected block rejection, got %v", header, err)
		}
	}
}

func TestPartialSelectionPreservesStandaloneBlockProperties(t *testing.T) {
	for _, property := range []string{"!!str", "!", "!<tag:yaml.org,2002:str>", "&script", "!!str &script", "&script !!str"} {
		t.Run(property, func(t *testing.T) {
			original := "runs-on: [self-hosted\nrun:\n  " + property + " |2-\n  if enabled:\n    sensitive_call()\nnext: unchanged\n\n"
			proposed := strings.Replace(original, "[self-hosted", "[self-hosted]", 1)
			proposed = strings.Replace(proposed, "    sensitive_call()", "  sensitive_call()", 1)
			proposed = strings.TrimSuffix(proposed, "\n")
			partial := proposed + "\n"
			if err := CheckSelection([]byte(original), []byte(proposed), []byte(partial)); err == nil || !strings.Contains(err.Error(), "literal block indentation") {
				t.Fatalf("expected block rejection, got %v", err)
			}
		})
	}
}
