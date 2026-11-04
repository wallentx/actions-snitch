// Package compat applies deterministic compatibility policy without side effects.
package compat

import (
	"regexp"
	"strconv"
	"strings"
)

var badgeScore = regexp.MustCompile(`<title>compatibility: ([0-9]+)%`)

// Score returns the public numeric-or-Unknown score representation.
func Score(raw string) any {
	if raw == "" {
		return "Unknown"
	}
	raw = strings.TrimLeft(raw, "0")
	if raw == "" {
		raw = "0"
	}
	if len(raw) > 3 {
		return "Unknown"
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return "Unknown"
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n > 100 {
		return "Unknown"
	}
	return n
}

func BadgeScore(svg string) any {
	matches := badgeScore.FindAllStringSubmatch(svg, -1)
	if len(matches) != 1 {
		return "Unknown"
	}
	return Score(matches[0][1])
}

type Decision struct {
	Allow  bool
	Assess bool
	Reason string
}

// Gate permits scores at the configured threshold. Below that threshold,
// force requests an AI investigation when enabled, otherwise a score override.
// Force never overrides the verified-creator requirement or AI approval.
func Gate(score any, verified, verifiedOnly, force, ai bool, threshold int) Decision {
	if verifiedOnly && !verified {
		return Decision{Reason: "creator is not verified"}
	}
	n, known := score.(int)
	known = known && n >= 0 && n <= 100
	if known && n >= threshold {
		return Decision{Allow: true}
	}
	if force {
		return Decision{Allow: !ai, Assess: ai, Reason: "compatibility requires review"}
	}
	return Decision{Reason: "compatibility requires review"}
}
