package app

import "testing"

func TestOptions(t *testing.T) {
	o, err := ParseOptions([]string{"-usfvv", "-btopic", "-o", "json"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.Update || !o.Pin || !o.Force || o.Verbosity != 2 || o.Branch != "topic" || o.Format != "json" {
		t.Fatalf("%+v", o)
	}
	for _, args := range [][]string{{"-f"}, {"-t"}, {"-b", "topic"}, {"-cu"}, {"-c", "ignored"}, {"-o", "xml"}} {
		if _, err := ParseOptions(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	for _, args := range [][]string{{"-h"}, {"-z"}, {"-b"}} {
		o, err := ParseOptions(args)
		if err != nil || !o.Help {
			t.Errorf("help: %v: %+v %v", args, o, err)
		}
	}
	o, err = ParseOptions([]string{"positional", "-u"})
	if err != nil || o.Update {
		t.Fatalf("getopts positional: %+v %v", o, err)
	}
}

func TestDiagnosticAndLintOptions(t *testing.T) {
	for _, args := range [][]string{{"--debug", "-u"}, {"-u", "--debug", "-o", "json"}, {"--lint", "--debug"}, {"--debug", "-c"}} {
		o, err := ParseOptions(args)
		if err != nil || !o.Debug {
			t.Fatalf("%v: %+v %v", args, o, err)
		}
	}
	if o, err := ParseOptions([]string{"--lint"}); err != nil || !o.Lint {
		t.Fatalf("lint: %+v %v", o, err)
	}
	for _, args := range [][]string{{"--lint", "-u"}, {"--lint", "-f"}, {"--lint", "--debug", "-p"}, {"--debug", "-cc"}} {
		if _, err := ParseOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
