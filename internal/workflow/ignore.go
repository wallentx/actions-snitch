package workflow

import (
	"regexp"
	"strings"
)

type ignoreRule struct {
	pattern []globToken
	include bool
}
type Ignore struct{ rules []ignoreRule }
type globToken struct {
	kind         rune
	literal      rune
	class        *regexp.Regexp
	alternatives [][]globToken
}

// ParseIgnore implements Bash conditional globs, including extended groups.
// Stars can span slashes, and later rules take precedence.
func ParseIgnore(data []byte) Ignore {
	var result Ignore
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		include := strings.HasPrefix(line, "!")
		if include {
			line = line[1:]
		}
		line = strings.TrimPrefix(strings.TrimPrefix(line, "./"), "/")
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "/") {
			line += "*"
		}
		result.rules = append(result.rules, ignoreRule{parseGlob([]rune(line)), include})
	}
	return result
}

func (i Ignore) Matches(path string) bool {
	ignored := false
	for _, rule := range i.rules {
		if globMatches(rule.pattern, []rune(path)) {
			ignored = !rule.include
		}
	}
	return ignored
}

func parseGlob(pattern []rune) []globToken {
	var result []globToken
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if strings.ContainsRune("@?*+!", ch) && i+1 < len(pattern) && pattern[i+1] == '(' {
			if parts, end, ok := globGroup(pattern, i+2); ok {
				token := globToken{kind: ch, alternatives: make([][]globToken, len(parts))}
				for j, p := range parts {
					token.alternatives[j] = parseGlob(p)
				}
				result = append(result, token)
				i = end
				continue
			}
		}
		token := globToken{kind: 'l', literal: ch}
		switch ch {
		case '\\':
			if i+1 < len(pattern) {
				i++
				token.literal = pattern[i]
			}
		case '*', '?':
			token.kind = ch
		case '[':
			if class, end, ok := globClass(pattern, i); ok {
				token.kind = 'c'
				token.class = class
				i = end
			}
		}
		result = append(result, token)
	}
	return result
}

func globClass(pattern []rune, start int) (*regexp.Regexp, int, bool) {
	end := start + 1
	if end < len(pattern) && (pattern[end] == '!' || pattern[end] == '^') {
		end++
	}
	if end < len(pattern) && pattern[end] == ']' {
		end++
	}
	for end < len(pattern) {
		if pattern[end] == '[' && end+1 < len(pattern) && pattern[end+1] == ':' {
			end += 2
			for end+1 < len(pattern) && (pattern[end] != ':' || pattern[end+1] != ']') {
				end++
			}
			end += 2
			continue
		}
		if pattern[end] == ']' {
			break
		}
		end++
	}
	if end >= len(pattern) {
		return nil, 0, false
	}
	class := string(pattern[start : end+1])
	if strings.HasPrefix(class, "[!") {
		class = "[^" + class[2:]
	}
	compiled, err := regexp.Compile("^(?:" + class + ")$")
	return compiled, end, err == nil
}

func globGroup(pattern []rune, start int) ([][]rune, int, bool) {
	depth := 1
	part := start
	var parts [][]rune
	for i := start; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			if _, end, ok := globClass(pattern, i); ok {
				i = end
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				parts = append(parts, pattern[part:i])
				return parts, i, true
			}
		case '|':
			if depth == 1 {
				parts = append(parts, pattern[part:i])
				part = i + 1
			}
		}
	}
	return nil, 0, false
}

func globMatches(tokens []globToken, input []rune) bool {
	ends := globEnds(tokens, input, 0)
	return ends[len(input)]
}

// globEnds uses sets of reachable offsets instead of exponential backtracking.
func globEnds(tokens []globToken, input []rune, start int) map[int]bool {
	positions := map[int]bool{start: true}
	for _, token := range tokens {
		next := map[int]bool{}
		for pos := range positions {
			if token.alternatives != nil {
				matches := func(from int) map[int]bool {
					r := map[int]bool{}
					for _, alternative := range token.alternatives {
						for end := range globEnds(alternative, input, from) {
							r[end] = true
						}
					}
					return r
				}
				ends := matches(pos)
				switch token.kind {
				case '!':
					for end := pos; end <= len(input); end++ {
						if !ends[end] {
							next[end] = true
						}
					}
				case '@':
					for end := range ends {
						next[end] = true
					}
				case '?':
					next[pos] = true
					for end := range ends {
						next[end] = true
					}
				case '*', '+':
					if token.kind == '*' {
						next[pos] = true
					}
					queue := []int{}
					seen := map[int]bool{}
					for end := range ends {
						queue = append(queue, end)
						seen[end] = true
						next[end] = true
					}
					for len(queue) > 0 {
						from := queue[0]
						queue = queue[1:]
						for end := range matches(from) {
							if !seen[end] {
								seen[end] = true
								next[end] = true
								queue = append(queue, end)
							}
						}
					}
				}
				continue
			}
			switch token.kind {
			case '*':
				for end := pos; end <= len(input); end++ {
					next[end] = true
				}
			case '?':
				if pos < len(input) {
					next[pos+1] = true
				}
			case 'c':
				if pos < len(input) && token.class.MatchString(string(input[pos])) {
					next[pos+1] = true
				}
			default:
				if pos < len(input) && input[pos] == token.literal {
					next[pos+1] = true
				}
			}
		}
		positions = next
		if len(positions) == 0 {
			break
		}
	}
	return positions
}
