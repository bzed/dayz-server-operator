// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"strings"
	"testing"
)

// rapNode describes one class for buildRap. There is no real config.bin
// fixture in this environment; buildRap follows the layout documented in
// rap.go, so these tests pin the decoder to that description only.
type rapNode struct {
	name     string
	parent   string
	entries  [][]byte // pre-encoded non-class entries
	children []rapNode
}

func rcs(s string) []byte { return append([]byte(s), 0) }

func rcInt(n int) []byte {
	var out []byte
	for {
		c := byte(n & 0x7f)
		n >>= 7
		if n > 0 {
			out = append(out, c|0x80)
			continue
		}
		return append(out, c)
	}
}

func rU32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func rString(name, val string) []byte {
	return bytes.Join([][]byte{{1, 0}, rcs(name), rcs(val)}, nil)
}

func rInt(name string, v int32) []byte {
	return bytes.Join([][]byte{{1, 2}, rcs(name), rU32(uint32(v))}, nil) //nolint:gosec // test
}

func rFloat(name string, v float32) []byte {
	return bytes.Join([][]byte{{1, 1}, rcs(name), rU32(math.Float32bits(v))}, nil)
}

// rArrayBody encodes elements: string, int32, float32, or []any (nested).
func rArrayBody(vals []any) []byte {
	out := rcInt(len(vals))
	for _, v := range vals {
		switch x := v.(type) {
		case string:
			out = append(out, 0)
			out = append(out, rcs(x)...)
		case float32:
			out = append(out, 1)
			out = append(out, rU32(math.Float32bits(x))...)
		case int32:
			out = append(out, 2)
			out = append(out, rU32(uint32(x))...) //nolint:gosec // test
		case []any:
			out = append(out, 3)
			out = append(out, rArrayBody(x)...)
		}
	}
	return out
}

func rArray(name string, vals ...any) []byte {
	return bytes.Join([][]byte{{2}, rcs(name), rArrayBody(vals)}, nil)
}

// encodeBody lays out n's body at absolute offset base: own entries, then
// each child's body in order.
func encodeBody(n rapNode, base int) []byte {
	head := append(rcs(n.parent), rcInt(len(n.entries)+len(n.children))...)
	for _, e := range n.entries {
		head = append(head, e...)
	}
	off := base + len(head)
	for _, c := range n.children {
		off += 1 + len(c.name) + 1 + 4
	}
	var tail []byte
	for _, c := range n.children {
		body := encodeBody(c, off)
		head = append(head, 0)
		head = append(head, rcs(c.name)...)
		head = append(head, rU32(uint32(off))...) //nolint:gosec // test
		off += len(body)
		tail = append(tail, body...)
	}
	return append(head, tail...)
}

func buildRap(root rapNode) []byte {
	out := append([]byte{}, rapMagic...)
	out = append(out, rU32(0)...)
	out = append(out, rU32(8)...)
	out = append(out, rU32(0)...)
	return append(out, encodeBody(root, 16)...)
}

func patchesRoot(reqs ...any) rapNode {
	return rapNode{children: []rapNode{{
		name: "CfgPatches",
		children: []rapNode{
			{name: "ModA", entries: [][]byte{rArray("requiredAddons", reqs...), rArray("units")}},
			{name: "ModB", entries: [][]byte{rArray("requiredAddons", "ModA")}},
		},
	}, {name: "CfgVehicles"}}}
}

func TestParseRapifiedPatches(t *testing.T) {
	data := buildRap(patchesRoot("DZ_Data", "DZ_Scripts"))
	if !IsRapified(data) {
		t.Fatal("IsRapified = false")
	}
	patches, err := ExtractPatchesFromConfig(data)
	if err != nil {
		t.Fatalf("ExtractPatchesFromConfig: %v", err)
	}
	got := map[string][]string{}
	for _, p := range patches {
		got[p.Name] = p.RequiredAddons
	}
	want := map[string][]string{"ModA": {"DZ_Data", "DZ_Scripts"}, "ModB": {"ModA"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("patches = %v, want %v", got, want)
	}
}

func TestParseRapifiedValueKinds(t *testing.T) {
	ext := append([]byte{3}, rcs("Ext")...)
	del := append([]byte{4}, rcs("Gone")...)
	appendArr := bytes.Join([][]byte{{5}, rU32(1), rcs("arr"), rArrayBody([]any{int32(-2), float32(1.5), []any{"x"}})}, nil)
	expr := bytes.Join([][]byte{{1, 4}, rcs("e"), rcs("1+1")}, nil)
	root, err := ParseRapified(buildRap(rapNode{
		parent:  "Base",
		entries: [][]byte{rString("s", "hi"), rInt("i", -7), rFloat("f", 0.25), ext, del, appendArr, expr},
	}))
	if err != nil {
		t.Fatalf("ParseRapified: %v", err)
	}
	if root.Parent != "Base" {
		t.Errorf("parent = %q", root.Parent)
	}
	for k, v := range map[string]string{"s": "hi", "i": "-7", "f": "0.25", "e": "1+1"} {
		if got := root.Properties[k]; got.IsArray || got.Scalar != v {
			t.Errorf("%s = %+v, want %q", k, got, v)
		}
	}
	if got := root.Properties["arr"]; !got.IsArray || !reflect.DeepEqual(got.Array, []string{"-2", "1.5", "x"}) {
		t.Errorf("arr = %+v", got)
	}
}

func nestedArrays(depth int) []byte {
	out := rcInt(1)
	for i := 0; i < depth; i++ {
		out = append(out, 3)
		out = append(out, rcInt(1)...)
	}
	return out
}

func TestParseRapifiedErrors(t *testing.T) {
	good := buildRap(patchesRoot("A"))
	if _, err := ParseRapified([]byte("class X {};")); err == nil {
		t.Error("expected error for non-rapified data")
	}
	for n := 16; n < len(good); n += 3 {
		if _, err := ParseRapified(good[:n]); err == nil {
			t.Errorf("no error for data truncated to %d bytes", n)
		}
	}
	bad := func(entry []byte) []byte {
		return buildRap(rapNode{entries: [][]byte{entry}})
	}
	for name, e := range map[string][]byte{
		"entry type":   {9},
		"value type":   append([]byte{1, 9}, rcs("x")...),
		"array elem":   bytes.Join([][]byte{{2}, rcs("a"), rcInt(1), {9}}, nil),
		"long varint":  bytes.Join([][]byte{{2}, rcs("a"), {0xff, 0xff, 0xff, 0xff, 0xff}}, nil),
		"deep nesting": bytes.Join([][]byte{{2}, rcs("a"), nestedArrays(rapMaxDepth + 2)}, nil),
	} {
		if _, err := ParseRapified(bad(e)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	// A class whose body offset points at its own parent recurses forever
	// without a depth limit.
	loop := buildRap(rapNode{})[:16]
	loop = append(loop, rcs("")...)
	loop = append(loop, rcInt(1)...)
	loop = append(loop, 0)
	loop = append(loop, rcs("C")...)
	loop = append(loop, rU32(16)...)
	if _, err := ParseRapified(loop); err == nil || !strings.Contains(err.Error(), "deeply") {
		t.Errorf("loop err = %v", err)
	}
	// Class offset beyond the data.
	far := buildRap(rapNode{})[:16]
	far = append(far, rcs("")...)
	far = append(far, rcInt(1)...)
	far = append(far, 0)
	far = append(far, rcs("C")...)
	far = append(far, rU32(9999)...)
	if _, err := ParseRapified(far); err == nil {
		t.Error("expected error for out-of-range class offset")
	}
}

func TestDecompressLZSS(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		size int
		want string
	}{
		{"literals and overlapping copy", []byte{7, 'a', 'b', 'c', 3, 3}, 9, "abcabcabc"},
		{"a distance of one repeats the last byte", []byte{1, 'x', 1, 0}, 4, "xxxx"},
		{"bytes before the start are spaces", []byte{0, 0xFA, 0xF0}, 3, "   "},
		{"output capped at size", []byte{7, 'a', 'b', 'c', 3, 3}, 5, "abcab"},
		// The start of a real config.bin: the zero padding is back references of distance 1 and 5.
		{"rapified header from a real mod", []byte{0x5f, 0, 'r', 'a', 'P', 0, 1, 0, 8, 5, 0}, 12, "\x00raP\x00\x00\x00\x00\x08\x00\x00\x00"},
		{"ignores trailing checksum", []byte{3, 'h', 'i', 1, 2, 3, 4}, 2, "hi"},
	}
	for _, c := range cases {
		got, err := decompressLZSS(c.in, c.size)
		if err != nil || string(got) != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := decompressLZSS([]byte{0, 0, 0}, 3); err == nil {
		t.Error("no error for a back reference of distance 0")
	}
	for _, in := range [][]byte{{}, {1}, {0, 1}} {
		if _, err := decompressLZSS(in, 4); err == nil {
			t.Errorf("no error for truncated input %v", in)
		}
	}
}

// lzssLiterals encodes data as literals only (valid LZSS, never compresses).
func lzssLiterals(data []byte) []byte {
	var out []byte
	for i := 0; i < len(data); i += 8 {
		end := min(i+8, len(data))
		out = append(out, byte(1<<(end-i)-1))
		out = append(out, data[i:end]...)
	}
	return append(out, 0, 0, 0, 0)
}

func TestPBOCompressedConfigBin(t *testing.T) {
	bin := buildRap(patchesRoot("DZ_Data"))
	data := buildPBO(t, true, []pboEntry{
		{name: `Config.BIN`, packing: packingCompressed, data: lzssLiterals(bin), original: uint32(len(bin))}, //nolint:gosec // test
	})
	pbo, err := OpenPBO(data)
	if err != nil {
		t.Fatalf("OpenPBO: %v", err)
	}
	patches, err := ExtractPatchesFromPBO(pbo)
	if err != nil {
		t.Fatalf("ExtractPatchesFromPBO: %v", err)
	}
	if len(patches) != 2 {
		t.Errorf("patches = %v", patches)
	}
}

func TestReadEntryCompressedErrors(t *testing.T) {
	data := buildPBO(t, false, []pboEntry{
		{name: "a", packing: packingCompressed, data: []byte{7, 'a'}, original: 50},
		{name: "b", packing: packingCompressed, data: []byte{0}, original: maxEntrySize + 1},
	})
	pbo, err := OpenPBO(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b"} {
		e, _ := pbo.Find(n)
		if _, err := pbo.ReadEntry(e); err == nil {
			t.Errorf("entry %s: expected error", n)
		}
	}
	e, _ := pbo.Find("a")
	e.DataSize = 1 << 20
	if _, err := pbo.ReadEntry(e); err == nil {
		t.Error("expected error for entry past end of PBO")
	}
}

func TestFindNormalizesNames(t *testing.T) {
	pbo, err := OpenPBO(buildPBO(t, false, []pboEntry{{name: `Scripts\3_Game\X.c`, data: []byte("x")}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pbo.Find("scripts/3_game/x.c"); !ok {
		t.Error("Find should ignore case and separator style")
	}
}

// Subtype 6 is an int64 (a Steam id in CfgMods of real workshop mods).
func TestRapInt64Value(t *testing.T) {
	id := bytes.Join([][]byte{{1, 6}, rcs("authorID"), binary.LittleEndian.AppendUint64(nil, 0x0110000148d61cbb)}, nil)
	root, err := ParseRapified(buildRap(rapNode{entries: [][]byte{id}}))
	if err != nil {
		t.Fatalf("ParseRapified: %v", err)
	}
	if got := root.Properties["authorID"].Scalar; got != "76561199182257339" {
		t.Errorf("authorID = %q", got)
	}
	short := append(buildRap(rapNode{entries: [][]byte{id}}), 0)
	if _, err := ParseRapified(short[:len(short)-5]); err == nil {
		t.Error("no error for a truncated int64")
	}
}

// Terrain and object packs keep a config per subfolder; all of them count, config.cpp only where a
// folder has no config.bin.
func TestExtractPatchesFromPBOSubfolders(t *testing.T) {
	one := func(name string) []byte {
		return buildRap(rapNode{children: []rapNode{{name: "CfgPatches", children: []rapNode{{name: name, entries: [][]byte{rArray("requiredAddons", "DZ_Data")}}}}}})
	}
	data := buildPBO(t, false, []pboEntry{
		{name: `bushes\config.bin`, data: one("Bushes")},
		{name: `Plants/CONFIG.BIN`, data: one("Plants")},
		{name: `plants/config.cpp`, data: []byte(`class CfgPatches { class Ignored { }; };`)},
		{name: `trees\config.cpp`, data: []byte(`class CfgPatches { class Trees { requiredAddons[] = {"DZ_Data"}; }; };`)},
		{name: `data\readme.txt`, data: []byte("x")},
	})
	pbo, err := OpenPBO(data)
	if err != nil {
		t.Fatal(err)
	}
	patches, err := ExtractPatchesFromPBO(pbo)
	if err != nil {
		t.Fatalf("ExtractPatchesFromPBO: %v", err)
	}
	names := map[string]bool{}
	for _, p := range patches {
		names[p.Name] = true
	}
	if len(patches) != 3 || !names["Bushes"] || !names["Plants"] || !names["Trees"] {
		t.Errorf("patches = %+v", patches)
	}
}

// Some tools store LZSS data in an entry whose packing method says "stored".
func TestExtractPatchesFromUnflaggedLZSS(t *testing.T) {
	bin := buildRap(patchesRoot("DZ_Data"))
	data := buildPBO(t, false, []pboEntry{{name: `VanillaItems\config.bin`, data: lzssLiterals(bin)}})
	pbo, err := OpenPBO(data)
	if err != nil {
		t.Fatal(err)
	}
	patches, err := ExtractPatchesFromPBO(pbo)
	if err != nil || len(patches) != 2 {
		t.Errorf("patches = %v, %v", patches, err)
	}
	got, err := decompressLZSS(lzssLiterals([]byte("hello")), -1)
	if err != nil || string(got) != "hello" {
		t.Errorf("unsized decode = %q, %v", got, err)
	}
}
