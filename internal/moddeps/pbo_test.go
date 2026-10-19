// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moddeps

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pboEntry is one file to embed via buildPBO.
type pboEntry struct {
	name     string
	packing  uint32
	data     []byte
	original uint32 // defaults to len(data) if zero
}

// buildPBO hand-assembles a minimal, valid PBO byte stream: an optional
// "Vers" product-info entry, one header record per entry, the table's
// terminating all-zero record, then each entry's data concatenated in
// order - the same layout OpenPBO parses. There is no real Bohemia PBO
// fixture available in this environment to test against instead (see the
// package doc comment); this construction follows the widely documented
// PBO container layout.
func buildPBO(t testing.TB, withVersEntry bool, entries []pboEntry) []byte {
	t.Helper()
	var buf bytes.Buffer

	writeCString := func(s string) {
		buf.WriteString(s)
		buf.WriteByte(0)
	}
	writeUint32 := func(v uint32) {
		err := binary.Write(&buf, binary.LittleEndian, v)
		require.NoError(t, err, "write uint32")
	}

	if withVersEntry {
		writeCString("")
		writeUint32(0x56657273) // "Vers"
		writeUint32(0)
		writeUint32(0)
		writeUint32(0)
		writeUint32(0)
		writeCString("product")
		writeCString("dzo-test")
		writeCString("prefix")
		writeCString(`dzo\test`)
		writeCString("") // terminates the product-info pairs
	}

	for _, e := range entries {
		writeCString(e.name)
		writeUint32(e.packing)
		original := e.original
		if original == 0 {
			original = uint32(len(e.data)) //nolint:gosec // test fixture, data is always small
		}
		writeUint32(original)
		writeUint32(0)                   // reserved
		writeUint32(0)                   // timestamp
		writeUint32(uint32(len(e.data))) //nolint:gosec // test fixture, data is always small
	}
	// Terminating entry.
	writeCString("")
	writeUint32(0)
	writeUint32(0)
	writeUint32(0)
	writeUint32(0)
	writeUint32(0)

	for _, e := range entries {
		buf.Write(e.data)
	}
	return buf.Bytes()
}

func TestOpenPBOAndReadEntry(t *testing.T) {
	data := buildPBO(t, false, []pboEntry{
		{name: "config.cpp", data: []byte(`class CfgPatches { class M { requiredAddons[] = {"A"}; }; };`)},
		{name: "readme.txt", data: []byte("hello")},
	})
	pbo, err := OpenPBO(data)
	require.NoError(t, err, "OpenPBO")
	require.Len(t, pbo.Entries, 2)

	entry, ok := pbo.Find("config.cpp")
	require.True(t, ok, "config.cpp not found")
	got, err := pbo.ReadEntry(entry)
	require.NoError(t, err, "ReadEntry")
	assert.True(t, bytes.Contains(got, []byte("CfgPatches")))

	readme, ok := pbo.Find("readme.txt")
	require.True(t, ok, "readme.txt not found")
	got2, err := pbo.ReadEntry(readme)
	require.NoError(t, err, "ReadEntry")
	assert.Equal(t, "hello", string(got2))
}

func TestOpenPBOWithVersEntry(t *testing.T) {
	data := buildPBO(t, true, []pboEntry{
		{name: "config.cpp", data: []byte("class X {};")},
	})
	pbo, err := OpenPBO(data)
	require.NoError(t, err, "OpenPBO")
	require.Len(t, pbo.Entries, 1)
	assert.Equal(t, `dzo\test`, pbo.Prefix)
	entry, _ := pbo.Find("config.cpp")
	got, err := pbo.ReadEntry(entry)
	require.NoError(t, err, "ReadEntry")
	assert.Equal(t, "class X {};", string(got))
}

func TestOpenPBOFindMissing(t *testing.T) {
	data := buildPBO(t, false, []pboEntry{{name: "a.txt", data: []byte("x")}})
	pbo, err := OpenPBO(data)
	require.NoError(t, err, "OpenPBO")
	_, ok := pbo.Find("missing.txt")
	assert.False(t, ok)
}

func TestOpenPBOTruncatedDataErrors(t *testing.T) {
	_, err := OpenPBO([]byte{1, 2, 3})
	require.Error(t, err, "expected an error for truncated/invalid PBO data")
}

func TestOpenPBOEmptyArchive(t *testing.T) {
	data := buildPBO(t, false, nil)
	pbo, err := OpenPBO(data)
	require.NoError(t, err, "OpenPBO")
	assert.Empty(t, pbo.Entries)
}
