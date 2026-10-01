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
