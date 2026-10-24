// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import "strings"

// This is a direct port of Maven's org.apache.maven.artifact.versioning.ComparableVersion
// (maven-artifact 3.8/3.9, nested-list form). A version is parsed into a tree of numeric
// items, string qualifiers and sub-lists; trailing "null" items (0, "", "ga", ...) are
// normalised away, and two trees are compared item by item.

type cvItem interface {
	// cmp compares with o; o == nil means "the other list has no item here".
	cmp(o cvItem) int
	null() bool
}

type cvInt struct{ v string } // canonical decimal digits without leading zeros

type cvStr struct{ v string } // lower-cased qualifier, aliases already applied

type cvList struct{ items []cvItem }

var cvQualifiers = []string{"alpha", "beta", "milestone", "rc", "snapshot", "", "sp"}

const cvReleaseKey = "5" // index of "" in cvQualifiers

func cvQualKey(q string) string {
	for i, k := range cvQualifiers {
		if k == q {
			return string(rune('0' + i))
		}
	}
	return "7-" + q // unknown qualifiers sort after every known one, among themselves as strings
}

func newCvStr(v string, followedByDigit bool) *cvStr {
	if followedByDigit && len(v) == 1 {
		switch v[0] {
		case 'a':
			v = "alpha"
		case 'b':
			v = "beta"
		case 'm':
			v = "milestone"
		}
	}
	switch v {
	case "ga", "final", "release":
		v = ""
	case "cr":
		v = "rc"
	}
	return &cvStr{v}
}

func cmpDigits(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func (n *cvInt) null() bool { return n.v == "0" }

func (n *cvInt) cmp(o cvItem) int {
	if o == nil {
		if n.null() {
			return 0
		}
		return 1
	}
	switch x := o.(type) {
	case *cvInt:
		return cmpDigits(n.v, x.v)
	}
	return 1 // a number is newer than a qualifier or a sub-list
}

func (s *cvStr) null() bool { return cvQualKey(s.v) == cvReleaseKey }

func (s *cvStr) cmp(o cvItem) int {
	if o == nil {
		return strings.Compare(cvQualKey(s.v), cvReleaseKey)
	}
	switch x := o.(type) {
	case *cvStr:
		return strings.Compare(cvQualKey(s.v), cvQualKey(x.v))
	}
	return -1 // a qualifier is older than a number or a sub-list
}

func (l *cvList) null() bool { return len(l.items) == 0 }

func (l *cvList) cmp(o cvItem) int {
	if o == nil {
		for _, it := range l.items {
			if c := it.cmp(nil); c != 0 {
				return c
			}
		}
		return 0
	}
	switch x := o.(type) {
	case *cvInt:
		return -1
	case *cvStr:
		return 1
	case *cvList:
		for i := 0; i < len(l.items) || i < len(x.items); i++ {
			var le, re cvItem
			if i < len(l.items) {
				le = l.items[i]
			}
			if i < len(x.items) {
				re = x.items[i]
			}
			var c int
			if le == nil {
				c = -1 * re.cmp(nil)
			} else {
				c = le.cmp(re)
			}
			if c != 0 {
				return c
			}
		}
	}
	return 0
}

func (l *cvList) normalize() {
	for i := len(l.items) - 1; i >= 0; i-- {
		last := l.items[i]
		if last.null() {
			l.items = append(l.items[:i], l.items[i+1:]...)
		} else if _, isList := last.(*cvList); !isList {
			break
		}
	}
}

func cvParseItem(isDigit bool, s string) cvItem {
	if isDigit {
		s = strings.TrimLeft(s, "0")
		if s == "" {
			s = "0"
		}
		return &cvInt{s}
	}
	return newCvStr(s, false)
}

func parseComparable(version string) *cvList {
	version = strings.ToLower(version)
	list := &cvList{}
	root := list
	stack := []*cvList{list}
	push := func() {
		nl := &cvList{}
		list.items = append(list.items, nl)
		list = nl
		stack = append(stack, list)
	}
	isDigit := false
	start := 0
	for i := 0; i < len(version); i++ {
		c := version[i]
		switch {
		case c == '.':
			if i == start {
				list.items = append(list.items, &cvInt{"0"})
			} else {
				list.items = append(list.items, cvParseItem(isDigit, version[start:i]))
			}
			start = i + 1
		case c == '-':
			if i == start {
				list.items = append(list.items, &cvInt{"0"})
			} else {
				list.items = append(list.items, cvParseItem(isDigit, version[start:i]))
			}
			start = i + 1
			push()
		case c >= '0' && c <= '9':
			if !isDigit && i > start {
				list.items = append(list.items, newCvStr(version[start:i], true))
				start = i
				push()
			}
			isDigit = true
		default:
			if isDigit && i > start {
				list.items = append(list.items, cvParseItem(true, version[start:i]))
				start = i
				push()
			}
			isDigit = false
		}
	}
	if len(version) > start {
		list.items = append(list.items, cvParseItem(isDigit, version[start:]))
	}
	for len(stack) > 0 {
		stack[len(stack)-1].normalize()
		stack = stack[:len(stack)-1]
	}
	return root
}

// compareVersions orders two version strings exactly like Maven's ComparableVersion.
func compareVersions(a, b string) int {
	return parseComparable(a).cmp(parseComparable(b))
}
