package compat

import "testing"

func TestAmbiguousBadgeFailsClosed(t *testing.T) {
	score := BadgeScore("<title>compatibility: 95%</title><title>compatibility: 20%</title>")
	if score != "Unknown" || Gate(score, true, false, false, false, 80).Allow {
		t.Fatalf("ambiguous badge allowed: %v", score)
	}
}

func TestGates(t *testing.T) {
	cases := []struct {
		score                                    any
		threshold                                int
		ai, force, verified, only, allow, assess bool
	}{
		{80, 80, false, false, false, false, true, false}, {79, 80, false, false, true, false, false, false},
		{70, 50, true, false, true, false, false, false}, {90, 100, true, false, true, false, false, true},
		{"Unknown", 0, true, false, true, false, false, true}, {"Unknown", 80, true, true, true, false, true, false},
		{100, 80, true, true, false, true, false, false},
	}
	for _, c := range cases {
		d := Gate(c.score, c.verified, c.only, c.force, c.ai, c.threshold)
		if d.Allow != c.allow || d.Assess != c.assess {
			t.Fatalf("case %+v: %+v", c, d)
		}
	}
}

func TestScore(t *testing.T) {
	for raw, want := range map[string]any{"00080": 80, "0": 0, "100": 100, "101": "Unknown", "-1": "Unknown", "1.5": "Unknown", "Unknown": "Unknown"} {
		if got := Score(raw); got != want {
			t.Errorf("%q: %v != %v", raw, got, want)
		}
	}
}
