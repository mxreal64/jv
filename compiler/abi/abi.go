// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

// Package abi fingerprints the binary interface of compiled classes: everything a downstream
// module can observe when it compiles against them (class shape, non-private members, constant
// values, generic signatures, thrown exceptions, annotations) and nothing it cannot (method
// bodies, private members, line numbers, the layout of the constant pool).
package abi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	accPrivate = 0x0002
	accModule  = 0x8000
)

var errTruncated = errors.New("truncated class file")

// ErrInternal is returned by Fingerprint for classes that cannot be referenced from outside their
// compilation unit (private, local and anonymous classes). They are not part of the ABI.
var ErrInternal = errors.New("class is not visible outside its compilation unit")

// Signature computes the ABI fingerprint of all .class files in a directory. A class file that
// cannot be parsed contributes its full bytes, so the result errs on the side of rebuilding.
func Signature(classesDir string) (string, error) {
	if _, err := os.Stat(classesDir); os.IsNotExist(err) {
		return "", nil
	}

	var classFiles []string
	_ = filepath.WalkDir(classesDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".class") {
			classFiles = append(classFiles, p)
		}
		return nil
	})
	sort.Strings(classFiles)

	h := sha256.New()
	for _, cf := range classFiles {
		data, err := os.ReadFile(cf)
		if err != nil {
			return "", err
		}
		rel, _ := filepath.Rel(classesDir, cf)
		canon, err := Fingerprint(data)
		if errors.Is(err, ErrInternal) {
			continue
		}
		fmt.Fprintf(h, "file %s\n", filepath.ToSlash(rel))
		if err != nil {
			fmt.Fprintf(h, "raw %x\n", sha256.Sum256(data))
			continue
		}
		h.Write([]byte(canon))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type cpEntry struct {
	tag  int
	a, b int
	v    uint64
	s    string
}

type attr struct {
	name string
	data []byte
}

// pool resolves constant pool references.
type pool []cpEntry

func (p pool) utf(i int) string {
	if i > 0 && i < len(p) && p[i].tag == 1 {
		return p[i].s
	}
	return ""
}

func (p pool) class(i int) string {
	if i > 0 && i < len(p) && p[i].tag == 7 {
		return p.utf(p[i].a)
	}
	return ""
}

// constant renders a loadable constant (int, float, long, double, string or utf8) canonically.
func (p pool) constant(i int) string {
	if i <= 0 || i >= len(p) {
		return "?"
	}
	e := p[i]
	switch e.tag {
	case 1:
		return "U" + e.s
	case 3:
		return "I" + strconv.FormatUint(e.v, 10)
	case 4:
		return "F" + strconv.FormatUint(e.v, 10)
	case 5:
		return "J" + strconv.FormatUint(e.v, 10)
	case 6:
		return "D" + strconv.FormatUint(e.v, 10)
	case 8:
		return "S" + p.utf(e.a)
	}
	return "?"
}

// Fingerprint returns the canonical, deterministic ABI description of one class file.
func Fingerprint(data []byte) (string, error) {
	r := &reader{b: data}
	if r.u4() != 0xCAFEBABE {
		return "", errors.New("not a class file")
	}
	r.u2() // minor
	r.u2() // major
	n := r.u2()
	cp := make(pool, n)
	for i := 1; i < n; i++ {
		e := cpEntry{tag: r.u1()}
		switch e.tag {
		case 1:
			e.s = string(r.bytes(r.u2()))
		case 3, 4:
			e.v = uint64(r.u4())
		case 5, 6:
			hi := uint64(r.u4())
			e.v = hi<<32 | uint64(r.u4())
			cp[i] = e
			i++ // long and double take two slots
			continue
		case 7, 8, 16, 19, 20:
			e.a = r.u2()
		case 9, 10, 11, 12, 17, 18:
			e.a, e.b = r.u2(), r.u2()
		case 15:
			e.a, e.b = r.u1(), r.u2()
		default:
			return "", fmt.Errorf("unknown constant pool tag %d", e.tag)
		}
		cp[i] = e
		if r.err != nil {
			return "", r.err
		}
	}

	access := r.u2()
	if access&accModule != 0 {
		return "", errors.New("module descriptor") // hashed whole by the caller
	}
	this, super := cp.class(r.u2()), cp.class(r.u2())
	ifaces := make([]string, r.u2())
	for i := range ifaces {
		ifaces[i] = cp.class(r.u2())
	}
	head := fmt.Sprintf("class %s %d %s [%s]", this, access, super, strings.Join(ifaces, ","))

	var lines []string
	internal := false
	members := func(kind string) {
		count := r.u2()
		for i := 0; i < count && r.err == nil; i++ {
			acc, name, desc := r.u2(), cp.utf(r.u2()), cp.utf(r.u2())
			attrs := readAttrs(r, cp)
			if acc&accPrivate != 0 || name == "<clinit>" {
				continue
			}
			line := fmt.Sprintf("%s %d %s %s", kind, acc, name, desc)
			for _, a := range attrs {
				sub := &reader{b: a.data}
				switch a.name {
				case "ConstantValue":
					line += " =" + cp.constant(sub.u2())
				case "Signature":
					line += " sig=" + cp.utf(sub.u2())
				case "Exceptions":
					var ex []string
					for k, c := 0, sub.u2(); k < c && sub.err == nil; k++ {
						ex = append(ex, cp.class(sub.u2()))
					}
					line += " throws=" + strings.Join(ex, ",")
				case "Deprecated":
					line += " deprecated"
				case "AnnotationDefault":
					line += " default=" + elementValue(sub, cp)
				case "RuntimeVisibleAnnotations", "RuntimeInvisibleAnnotations":
					line += " " + a.name[:len(a.name)-len("Annotations")] + annotations(sub, cp)
				}
			}
			lines = append(lines, line)
		}
	}
	members("F")
	members("M")

	for _, a := range readAttrs(r, cp) {
		sub := &reader{b: a.data}
		switch a.name {
		case "Signature":
			lines = append(lines, "csig "+cp.utf(sub.u2()))
		case "Deprecated":
			lines = append(lines, "cdeprecated")
		case "RuntimeVisibleAnnotations", "RuntimeInvisibleAnnotations":
			lines = append(lines, "c"+a.name[:len(a.name)-len("Annotations")]+annotations(sub, cp))
		case "InnerClasses":
			for k, c := 0, sub.u2(); k < c && sub.err == nil; k++ {
				inner, outer, name, flags := cp.class(sub.u2()), cp.class(sub.u2()), cp.utf(sub.u2()), sub.u2()
				if inner == this && (outer == "" || flags&accPrivate != 0) {
					internal = true // anonymous/local class (no outer class) or private member class
				}
				// Only this class and its own member classes describe its interface; entries for
				// other classes it merely mentions (MethodHandles$Lookup, Map$Entry, ...) do not.
				if (inner == this || outer == this) && flags&accPrivate == 0 {
					lines = append(lines, fmt.Sprintf("inner %s %s %s %d", inner, outer, name, flags))
				}
			}
		case "PermittedSubclasses":
			for k, c := 0, sub.u2(); k < c && sub.err == nil; k++ {
				lines = append(lines, "permits "+cp.class(sub.u2()))
			}
		case "Record":
			for k, c := 0, sub.u2(); k < c && sub.err == nil; k++ {
				name, desc := cp.utf(sub.u2()), cp.utf(sub.u2())
				readAttrs(sub, cp)
				lines = append(lines, "record "+name+" "+desc)
			}
		}
	}
	if r.err != nil {
		return "", r.err
	}
	if internal {
		return "", ErrInternal
	}
	sort.Strings(lines)
	return head + "\n" + strings.Join(lines, "\n"), nil
}

// annotations renders a RuntimeVisible/InvisibleAnnotations attribute body canonically. Annotations
// are part of the interface: a changed @Retention or @Target on an annotation type, for example,
// changes what downstream classes contain.
func annotations(r *reader, cp pool) string {
	var out []string
	for k, c := 0, r.u2(); k < c && r.err == nil; k++ {
		out = append(out, annotation(r, cp))
	}
	return "[" + strings.Join(out, ";") + "]"
}

func annotation(r *reader, cp pool) string {
	typ := cp.utf(r.u2())
	var pairs []string
	for k, c := 0, r.u2(); k < c && r.err == nil; k++ {
		name := cp.utf(r.u2())
		pairs = append(pairs, name+"="+elementValue(r, cp))
	}
	return "@" + typ + "(" + strings.Join(pairs, ",") + ")"
}

func elementValue(r *reader, cp pool) string {
	tag := r.u1()
	switch tag {
	case 'B', 'C', 'D', 'F', 'I', 'J', 'S', 'Z', 's':
		return string(rune(tag)) + ":" + cp.constant(r.u2())
	case 'e':
		return "e:" + cp.utf(r.u2()) + "." + cp.utf(r.u2())
	case 'c':
		return "c:" + cp.utf(r.u2())
	case '@':
		return annotation(r, cp)
	case '[':
		var vals []string
		for k, c := 0, r.u2(); k < c && r.err == nil; k++ {
			vals = append(vals, elementValue(r, cp))
		}
		return "{" + strings.Join(vals, ",") + "}"
	}
	r.err = errTruncated // unknown tag: give up, the caller falls back to the raw bytes
	return "?"
}

func readAttrs(r *reader, cp pool) []attr {
	count := r.u2()
	var out []attr
	for i := 0; i < count && r.err == nil; i++ {
		name := cp.utf(r.u2())
		out = append(out, attr{name: name, data: r.bytes(int(r.u4()))})
	}
	return out
}

// reader is a bounds-checked big-endian reader; the first overrun sets err and every later
// read returns zero values.
type reader struct {
	b   []byte
	p   int
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || n < 0 || r.p+n > len(r.b) {
		r.err = errTruncated
		r.p = len(r.b)
		return nil
	}
	v := r.b[r.p : r.p+n]
	r.p += n
	return v
}

func (r *reader) bytes(n int) []byte { return r.take(n) }

func (r *reader) u1() int {
	if b := r.take(1); b != nil {
		return int(b[0])
	}
	return 0
}

func (r *reader) u2() int {
	if b := r.take(2); b != nil {
		return int(b[0])<<8 | int(b[1])
	}
	return 0
}

func (r *reader) u4() uint32 {
	if b := r.take(4); b != nil {
		return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	}
	return 0
}
