// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package shade

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

type RelocateRule struct {
	Pattern  string // e.g. "com.google.common"
	Shadedto string // e.g. "shaded.com.google.common"
	Excludes []string
}

func (r *RelocateRule) relocateString(s string) (string, bool) {
	// Pattern can be dots or slashes
	dotPat := strings.ReplaceAll(r.Pattern, "/", ".")
	slashPat := strings.ReplaceAll(r.Pattern, ".", "/")
	dotShaded := strings.ReplaceAll(r.Shadedto, "/", ".")
	slashShaded := strings.ReplaceAll(r.Shadedto, ".", "/")

	// Check excludes
	for _, exc := range r.Excludes {
		dExc := strings.ReplaceAll(exc, "/", ".")
		sExc := strings.ReplaceAll(exc, ".", "/")
		if strings.HasPrefix(s, dExc) || strings.HasPrefix(s, sExc) {
			return s, false
		}
	}

	if strings.Contains(s, slashPat) {
		return strings.ReplaceAll(s, slashPat, slashShaded), true
	}
	if strings.Contains(s, dotPat) {
		return strings.ReplaceAll(s, dotPat, dotShaded), true
	}
	return s, false
}

// RewriteResource rewrites string occurrences in a text resource file.
func RewriteResource(data []byte, ext string, rules []RelocateRule) ([]byte, bool, error) {
	str := string(data)
	changed := false
	for _, r := range rules {
		newStr, ch := r.relocateString(str)
		if ch {
			str = newStr
			changed = true
		}
	}
	return []byte(str), changed, nil
}

// RewriteClass rewrites class name references in a .class file's constant pool.
// Parses constant pool (JVM spec §4.4) and replaces CONSTANT_Utf8 entries matching rules.
func RewriteClass(data []byte, rules []RelocateRule) ([]byte, bool, error) {
	if len(data) < 10 {
		return data, false, fmt.Errorf("invalid class file (too short)")
	}
	magic := binary.BigEndian.Uint32(data[0:4])
	if magic != 0xCAFEBABE {
		return data, false, fmt.Errorf("invalid class magic: %x", magic)
	}

	minor := binary.BigEndian.Uint16(data[4:6])
	major := binary.BigEndian.Uint16(data[6:8])
	cpCount := binary.BigEndian.Uint16(data[8:10])

	type cpEntry struct {
		tag  byte
		data []byte // raw bytes of the constant (for Utf8, utf8 string)
	}

	entries := make([]cpEntry, cpCount)
	offset := 10
	anyChanged := false

	for i := 1; i < int(cpCount); i++ {
		if offset >= len(data) {
			return data, false, fmt.Errorf("unexpected EOF reading constant pool")
		}
		tag := data[offset]
		offset++
		switch tag {
		case 1: // CONSTANT_Utf8
			if offset+2 > len(data) {
				return data, false, fmt.Errorf("invalid utf8 constant")
			}
			uLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
			if offset+uLen > len(data) {
				return data, false, fmt.Errorf("invalid utf8 constant length")
			}
			utf8Bytes := data[offset : offset+uLen]
			offset += uLen

			// Apply relocation rules to utf8Bytes
			strVal := string(utf8Bytes)
			curStr := strVal
			for _, r := range rules {
				if newStr, ok := r.relocateString(curStr); ok {
					curStr = newStr
					anyChanged = true
				}
			}
			entries[i] = cpEntry{tag: 1, data: []byte(curStr)}

		case 3, 4: // CONSTANT_Integer, CONSTANT_Float
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+4]}
			offset += 4
		case 5, 6: // CONSTANT_Long, CONSTANT_Double (takes 2 entries)
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+8]}
			offset += 8
			i++ // skip next slot
		case 7: // CONSTANT_Class
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+2]}
			offset += 2
		case 8: // CONSTANT_String
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+2]}
			offset += 2
		case 9, 10, 11: // CONSTANT_Fieldref, Methodref, InterfaceMethodref
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+4]}
			offset += 4
		case 12: // CONSTANT_NameAndType
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+4]}
			offset += 4
		case 15: // CONSTANT_MethodHandle
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+3]}
			offset += 3
		case 16: // CONSTANT_MethodType
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+2]}
			offset += 2
		case 17, 18: // CONSTANT_Dynamic, CONSTANT_InvokeDynamic
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+4]}
			offset += 4
		case 19, 20: // CONSTANT_Module, CONSTANT_Package
			entries[i] = cpEntry{tag: tag, data: data[offset : offset+2]}
			offset += 2
		default:
			return data, false, fmt.Errorf("unknown constant pool tag: %d at index %d", tag, i)
		}
	}

	if !anyChanged {
		return data, false, nil
	}

	// Reconstruct the class file
	var buf bytes.Buffer
	buf.Grow(len(data) + 128)
	buf.Write(data[0:4]) // magic
	_ = binary.Write(&buf, binary.BigEndian, minor)
	_ = binary.Write(&buf, binary.BigEndian, major)
	_ = binary.Write(&buf, binary.BigEndian, cpCount)

	for i := 1; i < int(cpCount); i++ {
		e := entries[i]
		if e.tag == 0 {
			continue // second slot of long/double
		}
		buf.WriteByte(e.tag)
		if e.tag == 1 {
			uLen := uint16(len(e.data))
			_ = binary.Write(&buf, binary.BigEndian, uLen)
			buf.Write(e.data)
		} else {
			buf.Write(e.data)
		}
	}

	// Append remainder of class file (access flags, interfaces, fields, methods, attributes)
	buf.Write(data[offset:])

	return buf.Bytes(), true, nil
}
