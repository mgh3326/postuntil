// Package predicate implements the intentionally small JSON predicate language.
package predicate

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Predicate struct {
	Path []segment
	Want any
}
type segment struct {
	key   string
	index *int
}

// Parse accepts paths such as .state=done and .items[0].state=true.
func Parse(s string) (Predicate, error) {
	parts := strings.SplitN(s, "=", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], ".") {
		return Predicate{}, fmt.Errorf("predicate must be .path=literal")
	}
	path, err := parsePath(parts[0])
	if err != nil {
		return Predicate{}, err
	}
	return Predicate{Path: path, Want: literal(parts[1])}, nil
}
func parsePath(s string) ([]segment, error) {
	var out []segment
	for len(s) > 0 {
		if s[0] != '.' {
			return nil, fmt.Errorf("invalid JSON path %q", s)
		}
		s = s[1:]
		i := 0
		for i < len(s) && s[i] != '.' && s[i] != '[' {
			i++
		}
		if i == 0 {
			return nil, fmt.Errorf("invalid JSON path")
		}
		out = append(out, segment{key: s[:i]})
		s = s[i:]
		for len(s) > 0 && s[0] == '[' {
			end := strings.IndexByte(s, ']')
			if end < 2 {
				return nil, fmt.Errorf("invalid array index")
			}
			n, e := strconv.Atoi(s[1:end])
			if e != nil || n < 0 {
				return nil, fmt.Errorf("invalid array index")
			}
			out = append(out, segment{index: &n})
			s = s[end+1:]
		}
	}
	return out, nil
}
func literal(s string) any {
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if s == "null" {
		return nil
	}
	var n json.Number
	if json.Unmarshal([]byte(s), &n) == nil {
		return n
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var v string
		if json.Unmarshal([]byte(s), &v) == nil {
			return v
		}
	}
	return s
}
func (p Predicate) Value(doc any) (any, bool) {
	v := doc
	for _, s := range p.Path {
		if s.index != nil {
			a, ok := v.([]any)
			if !ok || *s.index >= len(a) {
				return nil, false
			}
			v = a[*s.index]
		} else {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			var yes bool
			v, yes = m[s.key]
			if !yes {
				return nil, false
			}
		}
	}
	return v, true
}
func (p Predicate) Match(doc any) bool {
	v, ok := p.Value(doc)
	if !ok {
		return false
	}
	return equal(v, p.Want)
}
func equal(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }
