package llm

import "testing"

func TestLintResponseValidation(t *testing.T) {
	for _, response := range []string{`{"repair":null}`, `{"repair":"x","extra":1}`, `{"repair":"x","repair":"y"}`, `{"repair":"x"} junk`, `{"repair":"x"} {}`} {
		fake := &fakeModel{t: t, response: response}
		if _, err := (Service{Model: fake}).RepairLint(t.Context(), "opaque", "syntax error"); err == nil {
			t.Errorf("accepted %s", response)
		}
	}
	fake := &fakeModel{t: t, response: `{"repair":"jobs:\n"}`}
	got, err := (Service{Model: fake}).RepairLint(t.Context(), "jobs:", "syntax error")
	if err != nil || got != "jobs:\n" {
		t.Fatalf("%q %v", got, err)
	}
}
