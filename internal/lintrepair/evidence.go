// Package lintrepair prepares bounded, opaque evidence for structural YAML repairs.
package lintrepair

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const MaxBytes = 128 << 10

var words = regexp.MustCompile(`[A-Za-z0-9_]+(?:[.-][A-Za-z0-9_]+)*`)
var placeholder = regexp.MustCompile(`SNITCH_OPAQUE_[0-9]+`)
var valuePlaceholder = regexp.MustCompile(`SNITCH_VALUE_[0-9]+`)
var quoted = regexp.MustCompile(`'[^']*'|"[^"\\]*(?:\\.[^"\\]*)*"`)
var logicOperators = regexp.MustCompile(`==|!=|&&|\|\|`)
var repairSyntax = regexp.MustCompile(`\b(?:fromJSON|format)\b|\{0\}`)

func operands(s string) []string {
	return words.FindAllString(repairSyntax.ReplaceAllString(s, ""), -1)
}

// YAML permits either order for indentation and chomping indicators. Matching
// the final indicator also covers anchors/tags and standalone scalar headers.
var literalBlock = regexp.MustCompile(`(?:^|[ :])[|>](?:[1-9][+-]?|[+-][1-9]?)? *(?:#.*)?$`)

func blockBoundary(lines []string, index int) int {
	trim := strings.TrimLeft(lines[index], " ")
	indent := len(lines[index]) - len(trim)
	if strings.HasPrefix(trim, "|") || strings.HasPrefix(trim, ">") || strings.HasPrefix(trim, "!") || strings.HasPrefix(trim, "&") {
		// A standalone header, including one beginning with tag/anchor properties,
		// may share its indentation with the scalar content.
		// The preceding less-indented parent supplies the conservative boundary.
		for previous := index - 1; previous >= 0; previous-- {
			text := strings.TrimLeft(lines[previous], " ")
			n := len(lines[previous]) - len(text)
			if text != "" && n < indent {
				return n
			}
		}
		return -1
	}
	return indent
}

var structural = regexp.MustCompile(`^(?:- +)?(?:name|on|jobs|steps|runs-on|strategy|matrix|include|exclude|env|with|permissions|inputs|outputs|defaults|run|shell|working-directory|workflow_dispatch|push|pull_request|branches|paths|schedule|concurrency|container|services|needs|if|timeout-minutes|continue-on-error): *$`)
var runnerWords = map[string]bool{"runs-on": true, "github.repository": true, "fromJSON": true, "format": true, "0": true, "self-hosted": true, "ubuntu-latest": true, "ubuntu-24.04": true, "ubuntu-22.04": true, "windows-latest": true, "macos-latest": true, "linux": true, "Linux": true, "X64": true, "ARM64": true, "matrix.runner": true, "matrix.os": true, "true": true, "false": true}

// Evidence hides every line except narrowly recognized structural syntax. Opaque
// contents never leave the process, even when the input cannot be parsed as YAML.
type Evidence struct {
	Text   string
	opaque map[string]string
	values map[string]string
	atoms  []string
	blocks [][]string
}

func Prepare(source []byte) (*Evidence, error) {
	if len(source) == 0 || len(source) > MaxBytes || !utf8.Valid(source) || bytes.ContainsAny(source, "\x00\r\t") {
		return nil, errors.New("lint repair requires bounded UTF-8 YAML with spaces and LF line endings")
	}
	if placeholder.Match(source) || valuePlaceholder.Match(source) {
		return nil, errors.New("source conflicts with reserved lint placeholders")
	}
	e := &Evidence{opaque: map[string]string{}, values: map[string]string{}}
	lines := strings.Split(string(source), "\n")
	runnerIndent := -1
	blockStart, blockIndent := -1, -1
	var blockRanges [][2]int
	for i, line := range lines {
		trim := strings.TrimLeft(line, " ")
		indent := len(line) - len(trim)
		if blockStart >= 0 && trim != "" && indent <= blockIndent {
			blockRanges = append(blockRanges, [2]int{blockStart, i})
			blockStart = -1
		}
		if trim == "" {
			continue
		}
		if runnerIndent >= 0 && indent <= runnerIndent && !strings.HasPrefix(trim, "- ") {
			runnerIndent = -1
		}
		isRunner := strings.HasPrefix(trim, "runs-on:")
		if isRunner {
			runnerIndent = indent
		}
		safe := structural.MatchString(trim)
		if isRunner || runnerIndent >= 0 {
			safe = true
			masked := quoted.ReplaceAllStringFunc(trim, func(value string) string {
				if runnerWords[value[1:len(value)-1]] {
					return value
				}
				key := fmt.Sprintf("SNITCH_VALUE_%d", len(e.values))
				e.values[key] = value
				return "'" + key + "'"
			})
			originalTrim := trim
			trim = masked
			for _, word := range words.FindAllString(trim, -1) {
				if !runnerWords[word] && !valuePlaceholder.MatchString(word) {
					safe = false
				}
			}
			for _, r := range trim {
				if r > 127 || (!strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-. :[]>={} $'\"&|!(),", r)) {
					safe = false
				}
			}
			if safe {
				lines[i] = line[:indent] + trim
			} else {
				trim = originalTrim
			}
		}
		if blockStart >= 0 {
			safe = false
		}
		if blockStart < 0 && literalBlock.MatchString(trim) {
			blockStart = i
			blockIndent = blockBoundary(lines, i)
			safe = false
		}
		if !safe {
			key := fmt.Sprintf("SNITCH_OPAQUE_%d", len(e.opaque))
			e.opaque[key] = strings.TrimLeft(line, " ")
			lines[i] = line[:indent] + key
		}
	}
	if blockStart >= 0 {
		blockRanges = append(blockRanges, [2]int{blockStart, len(lines)})
	}
	for _, span := range blockRanges {
		e.blocks = append(e.blocks, slices.Clone(lines[span[0]:span[1]]))
	}
	e.Text = strings.Join(lines, "\n")
	e.atoms = operands(e.Text)
	return e, nil
}

// Restore permits syntax and indentation changes but prohibits literal changes,
// added/deleted/reordered opaque lines, and model-created workflow content.
func (e *Evidence) Restore(candidate string) ([]byte, error) {
	if len(candidate) > MaxBytes || !utf8.ValidString(candidate) || strings.ContainsAny(candidate, "\x00\r\t") {
		return nil, errors.New("invalid lint proposal encoding or size")
	}
	if !slices.Equal(e.atoms, operands(candidate)) || !slices.Equal(logicOperators.FindAllString(e.Text, -1), logicOperators.FindAllString(candidate, -1)) {
		return nil, errors.New("lint proposal changes protected workflow content")
	}
	lines := strings.Split(candidate, "\n")
	for _, block := range e.blocks {
		key := strings.TrimLeft(block[0], " ")
		start := -1
		for i, line := range lines {
			if strings.TrimLeft(line, " ") == key {
				start = i
				break
			}
		}
		if start < 0 || start+len(block) > len(lines) {
			return nil, errors.New("lint proposal changes a literal block")
		}
		delta := len(lines[start]) - len(block[0])
		for j, original := range block {
			line := lines[start+j]
			if strings.TrimLeft(original, " ") == "" {
				if line != original {
					return nil, errors.New("lint proposal changes literal block whitespace")
				}
				continue
			}
			if strings.TrimLeft(line, " ") != strings.TrimLeft(original, " ") || len(line)-len(original) != delta {
				return nil, errors.New("lint proposal changes literal block content or relative indentation")
			}
		}
	}
	for i, line := range lines {
		trim := strings.TrimLeft(line, " ")
		if value, ok := e.opaque[trim]; ok {
			lines[i] = line[:len(line)-len(trim)] + value
			continue
		}
		if placeholder.MatchString(line) {
			return nil, errors.New("lint proposal alters an opaque line")
		}
		// All unmasked characters must remain within the syntax alphabet.
		for _, r := range line {
			if r > 127 || !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-. :[]>={} $'\"&|!(),", r) {
				return nil, errors.New("lint proposal introduces unsupported syntax")
			}
		}
	}
	restored := strings.Join(lines, "\n")
	for key, value := range e.values {
		if !strings.Contains(e.Text, "'"+key+"'") {
			continue
		}
		needle := "'" + key + "'"
		if strings.Count(restored, needle) != 1 {
			return nil, errors.New("lint proposal alters an opaque scalar")
		}
		restored = strings.Replace(restored, needle, value, 1)
	}
	if valuePlaceholder.MatchString(restored) {
		return nil, errors.New("lint proposal contains an invalid opaque scalar")
	}
	return []byte(restored), nil
}
