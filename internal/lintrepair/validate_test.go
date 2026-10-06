package lintrepair

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestRealValidator(t *testing.T) {
	if _, err := exec.LookPath("actionlint"); err != nil {
		t.Skip("actionlint is unavailable")
	}
	root := t.TempDir()
	good := "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo secret-value\n        env:\n          PRIVATE: secret-value\n"
	if err := Check(t.Context(), root, "main.yml", []byte(good)); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(good, "          PRIVATE:", "        PRIVATE:", 1)
	diagnostics, err := Validate(t.Context(), root, "main.yml", []byte(bad))
	if err != nil || diagnostics == "" {
		t.Fatalf("%q %v", diagnostics, err)
	}
	if strings.Contains(diagnostics, "PRIVATE") || strings.Contains(diagnostics, "secret-value") {
		t.Fatal("diagnostic leaked source")
	}
	e, err := Prepare([]byte(bad))
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := e.Restore(strings.Replace(e.Text, "        SNITCH_OPAQUE_3", "          SNITCH_OPAQUE_3", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(t.Context(), root, "main.yml", repaired); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Validate(ctx, root, "main.yml", []byte(good)); err == nil {
		t.Fatal("cancelled validation succeeded")
	}
}

func TestMalformedRunnerExpression(t *testing.T) {
	if _, err := exec.LookPath("actionlint"); err != nil {
		t.Skip("actionlint is unavailable")
	}
	prefix := "on: push\njobs:\n  build:\n    strategy:\n      matrix:\n        runner: [linux]\n"
	bad := "    runs-on: ${{ github.repository == 'wallentx/lepton' && (- self-hosted\n    - Linux\n    - ARM64\n    - ${{ matrix.runner }}) || 'ubuntu-latest' }}\n"
	suffix := "    steps:\n      - run: echo private-secret\n"
	source := prefix + bad + suffix
	e, err := Prepare([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.Text, "wallentx") || strings.Contains(e.Text, "private-secret") {
		t.Fatal("source disclosed")
	}
	good := "    runs-on: >-\n      ${{ github.repository == 'SNITCH_VALUE_0'\n          && fromJSON(format('[\"self-hosted\",\"Linux\",\"ARM64\",\"{0}\"]', matrix.runner))\n          || 'ubuntu-latest' }}\n"
	start := strings.Index(e.Text, "    runs-on:")
	end := strings.Index(e.Text, "    steps:")
	candidate, err := e.Restore(e.Text[:start] + good + e.Text[end:])
	if err != nil {
		t.Fatalf("%v\n%s", err, e.Text)
	}
	if !strings.Contains(string(candidate), "'wallentx/lepton'") {
		t.Fatal("repository was not restored")
	}
	if err := Check(t.Context(), t.TempDir(), "main.yml", candidate); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitBlockIndentationIsProtectedBeyondLint(t *testing.T) {
	if _, err := exec.LookPath("actionlint"); err != nil {
		t.Skip("actionlint is unavailable")
	}
	source := "on: push\njobs:\n  build:\n    runs-on: [self-hosted\n    steps:\n      - shell: python\n        run: |2-\n          if enabled:\n            sensitive_call()\n"
	e, err := Prepare([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	repaired := strings.Replace(source, "[self-hosted", "[self-hosted]", 1)
	altered := strings.Replace(repaired, "            sensitive_call()", "          sensitive_call()", 1)
	if err := Check(t.Context(), t.TempDir(), "main.yml", []byte(altered)); err != nil {
		t.Fatalf("fixture should pass YAML/actionlint despite semantic damage: %v", err)
	}
	proposal := strings.Replace(e.Text, "[self-hosted", "[self-hosted]", 1)
	key := ""
	for token, value := range e.opaque {
		if value == "sensitive_call()" {
			key = token
		}
	}
	if key == "" {
		t.Fatal("body placeholder missing")
	}
	bad := strings.Replace(proposal, "            "+key, "          "+key, 1)
	if _, err := e.Restore(bad); err == nil {
		t.Fatal("lint-passing Python indentation damage accepted")
	}
}

func TestStandaloneTaggedBlockProtectedBeyondLint(t *testing.T) {
	if _, err := exec.LookPath("actionlint"); err != nil {
		t.Skip("actionlint is unavailable")
	}
	source := "on: push\njobs:\n  build:\n    runs-on: [self-hosted\n    steps:\n      - shell: python\n        run:\n          !!str |2-\n          if True:\n            print('PRIVATE')\n"
	e, err := Prepare([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	repaired := strings.Replace(source, "[self-hosted", "[self-hosted]", 1)
	altered := strings.Replace(repaired, "            print('PRIVATE')", "          print('PRIVATE')", 1)
	if err := Check(t.Context(), t.TempDir(), "main.yml", []byte(altered)); err != nil {
		t.Fatalf("fixture should pass YAML/actionlint despite semantic damage: %v", err)
	}
	proposal := strings.Replace(e.Text, "[self-hosted", "[self-hosted]", 1)
	key := ""
	for token, value := range e.opaque {
		if value == "print('PRIVATE')" {
			key = token
		}
	}
	if key == "" {
		t.Fatal("body placeholder missing")
	}
	if _, err := e.Restore(strings.Replace(proposal, "            "+key, "          "+key, 1)); err == nil {
		t.Fatal("tagged standalone block indentation damage accepted")
	}
}
