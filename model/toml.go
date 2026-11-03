// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package model

import (
	"fmt"
	"strconv"
	"strings"
)

// parseTOML parses the subset of TOML that jv's configuration files use: comments, [tables],
// [[arrays of tables]], and key = value with strings ("basic" with escapes, 'literal'), integers,
// floats, booleans and (possibly multi-line, nested) arrays. Inline tables, dates and multi-line
// strings are rejected with a clear error instead of being misread.
//
// Tables become map[string]any and arrays of tables become []map[string]any.
func parseTOML(data []byte) (map[string]any, error) {
	root := map[string]any{}
	cur := root
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		line := strings.TrimSpace(stripComment(lines[i]))
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "[["):
			if !strings.HasSuffix(line, "]]") {
				return nil, fmt.Errorf("line %d: unterminated [[table]] header", lineNo)
			}
			path, err := tablePath(line[2 : len(line)-2])
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			t, err := appendTable(root, path)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			cur = t
		case strings.HasPrefix(line, "["):
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: unterminated [table] header", lineNo)
			}
			path, err := tablePath(line[1 : len(line)-1])
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			t, err := openTable(root, path)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			cur = t
		default:
			eq := strings.Index(line, "=")
			if eq < 0 {
				return nil, fmt.Errorf("line %d: expected key = value, got %q", lineNo, line)
			}
			key, err := parseKey(strings.TrimSpace(line[:eq]))
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			rest := strings.TrimSpace(line[eq+1:])
			// an array may continue over several lines
			for depth(rest) > 0 && i+1 < len(lines) {
				i++
				rest += " " + strings.TrimSpace(stripComment(lines[i]))
			}
			if depth(rest) != 0 {
				return nil, fmt.Errorf("line %d: unterminated array", lineNo)
			}
			v, tail, err := parseValue(rest)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", lineNo, err)
			}
			if strings.TrimSpace(tail) != "" {
				return nil, fmt.Errorf("line %d: unexpected %q after value", lineNo, strings.TrimSpace(tail))
			}
			if _, dup := cur[key]; dup {
				return nil, fmt.Errorf("line %d: duplicate key %q", lineNo, key)
			}
			cur[key] = v
		}
	}
	return root, nil
}

// stripComment removes a trailing # comment that is not inside a string.
func stripComment(s string) string {
	var quote rune
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote && !(quote == '"' && i > 0 && s[i-1] == '\\') {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			return s[:i]
		}
	}
	return s
}

// depth is the number of unclosed [ in s, ignoring brackets inside strings.
func depth(s string) int {
	d := 0
	var quote rune
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote && !(quote == '"' && i > 0 && s[i-1] == '\\') {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '[':
			d++
		case r == ']':
			d--
		}
	}
	return d
}

func parseKey(k string) (string, error) {
	if k == "" {
		return "", fmt.Errorf("empty key")
	}
	if k[0] == '"' || k[0] == '\'' {
		v, tail, err := parseString(k)
		if err != nil || strings.TrimSpace(tail) != "" {
			return "", fmt.Errorf("bad quoted key %q", k)
		}
		return v, nil
	}
	for _, r := range k {
		if !(r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return "", fmt.Errorf("unsupported key %q (dotted keys are not supported; use a [table])", k)
		}
	}
	return k, nil
}

func tablePath(h string) ([]string, error) {
	var path []string
	for _, p := range strings.Split(h, ".") {
		k, err := parseKey(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		path = append(path, k)
	}
	return path, nil
}

// openTable returns the table at path, creating it; the last element of an array of tables is used
// for intermediate parts.
func openTable(root map[string]any, path []string) (map[string]any, error) {
	cur := root
	for _, p := range path {
		switch v := cur[p].(type) {
		case nil:
			t := map[string]any{}
			cur[p] = t
			cur = t
		case map[string]any:
			cur = v
		case []map[string]any:
			cur = v[len(v)-1]
		default:
			return nil, fmt.Errorf("%q is not a table", p)
		}
	}
	return cur, nil
}

func appendTable(root map[string]any, path []string) (map[string]any, error) {
	parent, err := openTable(root, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	last := path[len(path)-1]
	t := map[string]any{}
	switch v := parent[last].(type) {
	case nil:
		parent[last] = []map[string]any{t}
	case []map[string]any:
		parent[last] = append(v, t)
	default:
		return nil, fmt.Errorf("%q is not an array of tables", last)
	}
	return t, nil
}

// parseValue parses one value at the start of s and returns the rest.
func parseValue(s string) (any, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, "", fmt.Errorf("missing value")
	}
	switch {
	case s[0] == '"' || s[0] == '\'':
		if strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, `'''`) {
			return nil, "", fmt.Errorf("multi-line strings are not supported")
		}
		return parseString(s)
	case s[0] == '[':
		var out []any
		rest := strings.TrimSpace(s[1:])
		for {
			if rest == "" {
				return nil, "", fmt.Errorf("unterminated array")
			}
			if rest[0] == ']' {
				return out, rest[1:], nil
			}
			v, tail, err := parseValue(rest)
			if err != nil {
				return nil, "", err
			}
			out = append(out, v)
			rest = strings.TrimSpace(tail)
			if rest != "" && rest[0] == ',' {
				rest = strings.TrimSpace(rest[1:])
			} else if rest == "" || rest[0] != ']' {
				return nil, "", fmt.Errorf("expected , or ] in array")
			}
		}
	case s[0] == '{':
		return nil, "", fmt.Errorf("inline tables are not supported; use a [table]")
	}
	end := strings.IndexAny(s, ",]} \t")
	if end < 0 {
		end = len(s)
	}
	tok, tail := s[:end], s[end:]
	switch tok {
	case "true":
		return true, tail, nil
	case "false":
		return false, tail, nil
	}
	if n, err := strconv.ParseInt(strings.ReplaceAll(tok, "_", ""), 10, 64); err == nil {
		return n, tail, nil
	}
	if f, err := strconv.ParseFloat(strings.ReplaceAll(tok, "_", ""), 64); err == nil {
		return f, tail, nil
	}
	return nil, "", fmt.Errorf("cannot parse value %q (strings must be quoted)", tok)
}

func parseString(s string) (string, string, error) {
	q := s[0]
	if q == '\'' { // literal: no escapes
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", "", fmt.Errorf("unterminated string")
		}
		return s[1 : 1+end], s[end+2:], nil
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			return b.String(), s[i+1:], nil
		case c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"', '\\':
				b.WriteByte(s[i])
			default:
				return "", "", fmt.Errorf("unsupported escape \\%c", s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", "", fmt.Errorf("unterminated string")
}
