// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Rapified config.bin ("raP") decoder. Format source: the community
// documentation of Bohemia's binarized config (Arma wiki "Rapified
// config" notes, mikero's DePbo/DeRap, and the armake/armake2 readers).
// Layout, all little-endian:
//
//	"\0raP" uint32(0) uint32(8) uint32(enumTableOffset)
//	root class body at file offset 16
//	class body: cstring parent, compressed-int entryCount, entries
//	entry: byte type, then per type:
//	  0 class:        cstring name, uint32 absolute body offset
//	  1 value:        byte sub (0 string, 1 float32, 2 int32, 4 expression
//	                  string, 6 int64), cstring name, payload
//	  2 array:        cstring name, array
//	  3 extern class: cstring name
//	  4 delete class: cstring name
//	  5 array += / -=: uint32 flags, cstring name, array
//	array: compressed-int count, then per element a byte sub (0 string,
//	  1 float32, 2 int32, 3 nested array, 4 expression string) and payload
//	compressed int: 7 bits per byte, low group first, high bit = more.
//
// Only the tree needed for CfgPatches is kept (scalars and arrays as
// text). Written from the format notes, then run over the ~4900 PBOs of the
// workshop mods of a development machine: all but 3 decode (those use a value
// type that armake2 does not know either), and CfgPatches matches armake2's
// on a random sample of 311.

var rapMagic = []byte{0, 'r', 'a', 'P'}

const rapMaxDepth = 64

// IsRapified reports whether data starts with the rapified-config magic.
func IsRapified(data []byte) bool { return bytes.HasPrefix(data, rapMagic) }

type rapReader struct {
	b   []byte
	pos int
}

var errRapTruncated = errors.New("truncated rapified config")

func (r *rapReader) byte() (byte, error) {
	if r.pos >= len(r.b) {
		return 0, errRapTruncated
	}
	c := r.b[r.pos]
	r.pos++
	return c, nil
}

func (r *rapReader) u32() (uint32, error) {
	if r.pos+4 > len(r.b) {
		return 0, errRapTruncated
	}
	v := binary.LittleEndian.Uint32(r.b[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *rapReader) cstring() (string, error) {
	i := bytes.IndexByte(r.b[r.pos:], 0)
	if i < 0 {
		return "", errRapTruncated
	}
	s := string(r.b[r.pos : r.pos+i])
	r.pos += i + 1
	return s, nil
}

func (r *rapReader) compressedInt() (int, error) {
	v := 0
	for shift := uint(0); shift < 28; shift += 7 {
		c, err := r.byte()
		if err != nil {
			return 0, err
		}
		v |= int(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, nil
		}
	}
	return 0, errors.New("compressed int too long")
}

// scalar reads one payload of sub-type sub as text.
func (r *rapReader) scalar(sub byte) (string, error) {
	switch sub {
	case 0, 4:
		return r.cstring()
	case 1:
		v, err := r.u32()
		return strconv.FormatFloat(float64(math.Float32frombits(v)), 'g', -1, 32), err
	case 2:
		v, err := r.u32()
		return strconv.FormatInt(int64(int32(v)), 10), err //nolint:gosec // reinterpreting the on-disk int32 bits
	case 6: // an int64, e.g. a Steam id in CfgMods (seen in real workshop configs, not in the format notes)
		if r.pos+8 > len(r.b) {
			return "", errRapTruncated
		}
		v := binary.LittleEndian.Uint64(r.b[r.pos:])
		r.pos += 8
		return strconv.FormatInt(int64(v), 10), nil //nolint:gosec // reinterpreting the on-disk int64 bits
	}
	return "", fmt.Errorf("unknown value type %d", sub)
}

// array reads a count-prefixed array; nested arrays are flattened.
func (r *rapReader) array(depth int) ([]string, error) {
	if depth > rapMaxDepth {
		return nil, errors.New("arrays nested too deeply")
	}
	n, err := r.compressedInt()
	if err != nil {
		return nil, err
	}
	var out []string
	for ; n > 0; n-- {
		sub, err := r.byte()
		if err != nil {
			return nil, err
		}
		if sub == 3 {
			inner, err := r.array(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, inner...)
			continue
		}
		s, err := r.scalar(sub)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// ParseRapified decodes a rapified config.bin into the same Class tree
// ParseConfig produces.
func ParseRapified(data []byte) (*Class, error) {
	if !IsRapified(data) || len(data) < 16 {
		return nil, errors.New("moddeps: not a rapified config")
	}
	root := newClass("", "")
	if err := rapBody(data, 16, root, 0); err != nil {
		return nil, fmt.Errorf("moddeps: rapified config: %w", err)
	}
	return root, nil
}

func rapBody(data []byte, off int, into *Class, depth int) error {
	if depth > rapMaxDepth {
		return errors.New("classes nested too deeply")
	}
	if off < 0 || off > len(data) {
		return errRapTruncated
	}
	r := &rapReader{b: data, pos: off}
	parent, err := r.cstring()
	if err != nil {
		return err
	}
	into.Parent = parent
	n, err := r.compressedInt()
	if err != nil {
		return err
	}
	for ; n > 0; n-- {
		typ, err := r.byte()
		if err != nil {
			return err
		}
		switch typ {
		case 0:
			name, err := r.cstring()
			if err != nil {
				return err
			}
			bodyOff, err := r.u32()
			if err != nil {
				return err
			}
			c := newClass(name, "")
			if err := rapBody(data, int(bodyOff), c, depth+1); err != nil { //nolint:gosec // bounds-checked in rapBody
				return err
			}
			into.Classes[name] = c
		case 1:
			sub, err := r.byte()
			if err != nil {
				return err
			}
			name, err := r.cstring()
			if err != nil {
				return err
			}
			s, err := r.scalar(sub)
			if err != nil {
				return err
			}
			into.Properties[name] = Value{Scalar: s}
		case 2, 5:
			if typ == 5 {
				if _, err := r.u32(); err != nil { // flags
					return err
				}
			}
			name, err := r.cstring()
			if err != nil {
				return err
			}
			arr, err := r.array(0)
			if err != nil {
				return err
			}
			into.Properties[name] = Value{IsArray: true, Array: arr}
		case 3, 4:
			if _, err := r.cstring(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown entry type %d", typ)
		}
	}
	return nil
}
