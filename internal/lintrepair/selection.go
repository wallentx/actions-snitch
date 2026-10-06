package lintrepair

import (
	"bytes"
	"errors"
	"slices"
	"strings"
)

// CheckSelection accepts only original/proposed choices at line-diff hunks.
// The bounded LCS table keeps review work independent of model-generated size.
func CheckSelection(original, proposed, selected []byte) error {
	if len(original) > MaxBytes || len(proposed) > MaxBytes || len(selected) > MaxBytes {
		return errors.New("diff selection exceeds size limit")
	}
	if bytes.Equal(selected, original) || bytes.Equal(selected, proposed) {
		return nil
	}
	a, b, c := strings.Split(string(original), "\n"), strings.Split(string(proposed), "\n"), strings.Split(string(selected), "\n")
	if len(a)+1 > 4_000_000/(len(b)+1) {
		return errors.New("diff is too complex for partial acceptance; choose the complete proposal or original")
	}
	width := len(b) + 1
	table := make([]uint32, (len(a)+1)*width)
	at := func(i, j int) uint32 { return table[i*width+j] }
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i*width+j] = at(i+1, j+1) + 1
			} else {
				table[i*width+j] = max(at(i+1, j), at(i, j+1))
			}
		}
	}
	positions := map[int]bool{0: true}
	advance := func(ours, theirs []string) {
		next := map[int]bool{}
		for pos := range positions {
			for _, part := range [][]string{ours, theirs} {
				end := pos + len(part)
				if end <= len(c) && slices.Equal(c[pos:end], part) {
					next[end] = true
				}
			}
		}
		positions = next
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		if i < len(a) && j < len(b) && a[i] == b[j] {
			advance(a[i:i+1], a[i:i+1])
			i++
			j++
			continue
		}
		startA, startB := i, j
		for (i < len(a) || j < len(b)) && (i >= len(a) || j >= len(b) || a[i] != b[j]) {
			if j == len(b) || (i < len(a) && at(i+1, j) >= at(i, j+1)) {
				i++
			} else {
				j++
			}
		}
		advance(a[startA:i], b[startB:j])
	}
	if !positions[len(c)] {
		return errors.New("diff tool introduced changes outside the proposed hunks")
	}
	if !slices.Equal(operands(string(original)), operands(string(selected))) {
		return errors.New("partial selection changes protected workflow operands")
	}
	// A partial choice must not split a literal block's indentation shift.
	nextStart := 0
	for start := 0; start < len(a); start++ {
		if !literalBlock.MatchString(strings.TrimLeft(a[start], " ")) {
			continue
		}
		indent := blockBoundary(a, start)
		end := start + 1
		for end < len(a) {
			trim := strings.TrimLeft(a[end], " ")
			if trim != "" && len(a[end])-len(trim) <= indent {
				break
			}
			end++
		}
		key := strings.TrimLeft(a[start], " ")
		found := -1
		for k := nextStart; k < len(c); k++ {
			if strings.TrimLeft(c[k], " ") == key {
				found = k
				break
			}
		}
		if found < 0 || found+end-start > len(c) {
			return errors.New("partial selection changes a literal block")
		}
		delta := len(c[found]) - len(a[start])
		for offset, line := range a[start:end] {
			chosen := c[found+offset]
			if strings.TrimLeft(line, " ") == "" {
				if chosen != line {
					return errors.New("partial selection changes literal block whitespace")
				}
			} else if strings.TrimLeft(line, " ") != strings.TrimLeft(chosen, " ") || len(chosen)-len(line) != delta {
				return errors.New("partial selection changes literal block indentation")
			}
		}
		nextStart = found + end - start
		start = end - 1
	}
	return nil
}
