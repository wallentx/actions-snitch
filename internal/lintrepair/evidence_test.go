package lintrepair

import (
	"strings"
	"testing"
)

func TestOpaqueEvidenceAndRepair(t *testing.T) {
	source := "name: private-name\non: push\njobs:\n  build:\n    runs-on: [self-hosted\n    steps:\n      - uses: private/action@v1\n        with:\n          password: super-secret-value\n      - run: |\n          echo hidden-command\n          echo second-command\n"
	evidence, err := Prepare([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-name", "private/action", "super-secret-value", "hidden-command", "second-command"} {
		if strings.Contains(evidence.Text, secret) {
			t.Fatalf("exposed %s", secret)
		}
	}
	proposal := strings.Replace(evidence.Text, "[self-hosted", "[self-hosted]", 1)
	got, err := evidence.Restore(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.Replace(source, "[self-hosted", "[self-hosted]", 1) {
		t.Fatalf("unexpected restoration: %s", got)
	}
	for _, bad := range []string{strings.Replace(proposal, "self-hosted", "ubuntu-latest", 1), proposal + "steps:\n", strings.Replace(proposal, "SNITCH_OPAQUE_0", "SNITCH_OPAQUE_0:", 1), strings.Replace(proposal, "          SNITCH_OPAQUE_6", "        SNITCH_OPAQUE_6", 1)} {
		if _, err := evidence.Restore(bad); err == nil {
			t.Fatalf("accepted unsafe edit %q", bad)
		}
	}
}

func TestIndentationRepair(t *testing.T) {
	source := "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo private\n        env:\n        PRIVATE: secret\n"
	e, err := Prepare([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	proposal := strings.Replace(e.Text, "        SNITCH_OPAQUE_3", "          SNITCH_OPAQUE_3", 1)
	got, err := e.Restore(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "          PRIVATE: secret") {
		t.Fatalf("indentation not repaired: %s\n%s", got, e.Text)
	}
}

func TestEvidenceFailClosed(t *testing.T) {
	for _, source := range []string{"", "a:\tsecret", "a: secret\r\n", "SNITCH_OPAQUE_0", strings.Repeat("x", MaxBytes+1)} {
		if _, err := Prepare([]byte(source)); err == nil {
			t.Error("accepted unsafe source")
		}
	}
}

func TestBlockScalarHeaderFormsPreserveContent(t *testing.T) {
	headers := []string{"|", ">", "|-", "|+", ">-", ">+"}
	for digit := '1'; digit <= '9'; digit++ {
		for _, style := range []string{"|", ">"} {
			for _, chomp := range []string{"", "-", "+"} {
				headers = append(headers, style+string(digit)+chomp)
				if chomp != "" {
					headers = append(headers, style+chomp+string(digit))
				}
			}
		}
	}
	for _, header := range headers {
		for _, property := range []string{"", "&script ", "!!str ", "!!str &script "} {
			t.Run(property+header, func(t *testing.T) {
				source := "runs-on: [self-hosted\nrun: " + property + header + " # retain behavior\n    if enabled:\n      sensitive_call()\nnext: unchanged\n"
				e, err := Prepare([]byte(source))
				if err != nil {
					t.Fatal(err)
				}
				proposal := strings.Replace(e.Text, "[self-hosted", "[self-hosted]", 1)
				if _, err := e.Restore(proposal); err != nil {
					t.Fatalf("unchanged block rejected: %v", err)
				}
				bad := strings.Replace(proposal, "      SNITCH_OPAQUE_2", "    SNITCH_OPAQUE_2", 1)
				if _, err := e.Restore(bad); err == nil {
					t.Fatalf("accepted body dedent with %s", header)
				}
			})
		}
	}
}

func TestStandaloneBlockHeaderPreservesContent(t *testing.T) {
	source := "runs-on: [self-hosted\nrun:\n  |2-\n  if enabled:\n    sensitive_call()\nnext: unchanged\n"
	e, err := Prepare([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	proposal := strings.Replace(e.Text, "[self-hosted", "[self-hosted]", 1)
	if _, err := e.Restore(proposal); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Restore(strings.Replace(proposal, "    SNITCH_OPAQUE_2", "  SNITCH_OPAQUE_2", 1)); err == nil {
		t.Fatal("standalone block indentation changed")
	}
}

func TestStandaloneBlockPropertiesPreserveContent(t *testing.T) {
	for _, property := range []string{"!!str", "!", "!<tag:yaml.org,2002:str>", "&script", "!!str &script", "&script !!str"} {
		t.Run(property, func(t *testing.T) {
			source := "runs-on: [self-hosted\nrun:\n  " + property + " |2-\n  if enabled:\n    sensitive_call()\nnext: unchanged\n"
			e, err := Prepare([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			proposal := strings.Replace(e.Text, "[self-hosted", "[self-hosted]", 1)
			if _, err := e.Restore(proposal); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Restore(strings.Replace(proposal, "    SNITCH_OPAQUE_2", "  SNITCH_OPAQUE_2", 1)); err == nil {
				t.Fatal("standalone property block indentation changed")
			}
		})
	}
}
