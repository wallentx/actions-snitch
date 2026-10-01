package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type replacement struct {
	start, end int
	value      []byte
}

func offset(source []byte, line, column int) (int, error) {
	pos := 0
	for row := 1; row < line; row++ {
		next := bytes.IndexByte(source[pos:], '\n')
		if next < 0 {
			return 0, fmt.Errorf("YAML line is outside source")
		}
		pos += next + 1
	}
	for col := 1; col < column; col++ {
		if pos >= len(source) || source[pos] == '\n' {
			return 0, fmt.Errorf("YAML column is outside source")
		}
		_, size := utf8.DecodeRune(source[pos:])
		pos += size
	}
	return pos, nil
}

// scalarPatch replaces only the scalar bytes, retaining quotes, anchors, and comments.
func scalarPatch(source []byte, node *yaml.Node, value string) (replacement, error) {
	line, column := node.Line, node.Column
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		line++
		column = 1
	}
	start, err := offset(source, line, column)
	if err != nil {
		return replacement{}, err
	}
	end := bytes.IndexByte(source[start:], '\n')
	if end < 0 {
		end = len(source)
	} else {
		end += start
	}
	segment := string(source[start:end])
	oldRef := "@" + strings.SplitN(node.Value, "@", 2)[1]
	newRef := "@" + strings.SplitN(value, "@", 2)[1]
	if node.Style&yaml.DoubleQuotedStyle != 0 {
		begin := strings.IndexByte(segment, '"')
		if begin < 0 {
			return replacement{}, fmt.Errorf("quoted scalar missing")
		}
		finish := -1
		for i := begin + 1; i < len(segment); i++ {
			if segment[i] == '\\' {
				i++
				continue
			}
			if segment[i] == '"' {
				finish = i + 1
				break
			}
		}
		if finish < 0 {
			return replacement{}, fmt.Errorf("unterminated quoted scalar")
		}
		quoted := segment[begin:finish]
		if strings.Contains(quoted, oldRef) {
			quoted = strings.Replace(quoted, oldRef, newRef, 1)
		} else {
			b, e := json.Marshal(value)
			if e != nil {
				return replacement{}, e
			}
			quoted = string(b)
		}
		return replacement{start + begin, start + finish, []byte(quoted)}, nil
	}
	if node.Style&yaml.SingleQuotedStyle != 0 {
		begin := strings.IndexByte(segment, '\'')
		if begin < 0 {
			return replacement{}, fmt.Errorf("quoted scalar missing")
		}
		finish := -1
		for i := begin + 1; i < len(segment); i++ {
			if segment[i] == '\'' {
				if i+1 < len(segment) && segment[i+1] == '\'' {
					i++
					continue
				}
				finish = i + 1
				break
			}
		}
		if finish < 0 {
			return replacement{}, fmt.Errorf("unterminated scalar")
		}
		quoted := segment[begin:finish]
		if strings.Contains(quoted, oldRef) {
			quoted = strings.Replace(quoted, oldRef, newRef, 1)
		} else {
			quoted = "'" + strings.ReplaceAll(value, "'", "''") + "'"
		}
		return replacement{start + begin, start + finish, []byte(quoted)}, nil
	}
	begin := strings.Index(segment, node.Value)
	if begin < 0 {
		return replacement{}, fmt.Errorf("scalar does not occupy a safe single source span")
	}
	return replacement{start + begin, start + begin + len(node.Value), []byte(value)}, nil
}

func patchScalars(source []byte, changes map[*yaml.Node]string) ([]byte, error) {
	patches := make([]replacement, 0, len(changes))
	for node, value := range changes {
		p, err := scalarPatch(source, node, value)
		if err != nil {
			return nil, err
		}
		patches = append(patches, p)
	}
	sort.Slice(patches, func(i, j int) bool { return patches[i].start > patches[j].start })
	result := bytes.Clone(source)
	previous := len(source)
	for _, p := range patches {
		if p.start < 0 || p.end > previous {
			return nil, fmt.Errorf("overlapping YAML scalar edits")
		}
		result = append(append(append([]byte{}, result[:p.start]...), p.value...), result[p.end:]...)
		previous = p.start
	}
	return result, nil
}
