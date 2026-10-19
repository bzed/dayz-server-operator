// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package servercfg

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sample = `
// this is the hostname
hostname = "dzo test server";
maxPlayers = 60;
verifySignatures = 2;
disableVoN = 0;
beLoggerFilters[] = {"KillHacker", "TeleportHacker"};
motd[] = {"Welcome", "Have fun"};
/* block comment
   spanning lines */
template = "dayzOffline.chernarusplus";
`

func TestParseBasic(t *testing.T) {
	f, err := Parse([]byte(sample))
	require.NoError(t, err)

	wantKeys := []string{"hostname", "maxPlayers", "verifySignatures", "disableVoN", "beLoggerFilters", "motd", "template"}
	require.Equal(t, wantKeys, f.Keys())

	hostname, ok := f.Get("hostname")
	require.True(t, ok, "hostname not found")
	assert.Equal(t, "dzo test server", hostname.Scalar.Value)
	assert.True(t, hostname.Scalar.Quoted)

	maxPlayers, ok := f.Get("maxPlayers")
	require.True(t, ok, "maxPlayers not found")
	n, err := maxPlayers.AsInt()
	require.NoError(t, err)
	assert.Equal(t, 60, n)

	motd, ok := f.Get("motd")
	require.True(t, ok)
	require.True(t, motd.IsArray)
	require.Len(t, motd.Array, 2)
	assert.Equal(t, "Welcome", motd.Array[0].Value)
	assert.Equal(t, "Have fun", motd.Array[1].Value)
}

func TestAsIntOnArrayFails(t *testing.T) {
	f, err := Parse([]byte(sample))
	require.NoError(t, err)
	motd, _ := f.Get("motd")
	_, err = motd.AsInt()
	require.Error(t, err, "expected an error calling AsInt on an array entry")
}

func TestRoundTrip(t *testing.T) {
	f, err := Parse([]byte(sample))
	require.NoError(t, err)
	out := f.String()

	f2, err := Parse([]byte(out))
	require.NoError(t, err)
	require.Equal(t, f.Keys(), f2.Keys(), "keys changed across round trip")
	for _, k := range f.Keys() {
		e1, _ := f.Get(k)
		e2, _ := f2.Get(k)
		assert.Equal(t, e1.Render(), e2.Render(), "key %s after round trip", k)
	}
}

func TestSetScalarUpdatesInPlace(t *testing.T) {
	f, err := Parse([]byte(sample))
	require.NoError(t, err)
	before := f.Keys()
	f.SetScalar("maxPlayers", "100", false)
	after := f.Keys()
	require.Equal(t, before, after, "SetScalar on an existing key should not change key order")
	e, _ := f.Get("maxPlayers")
	assert.Equal(t, "100", e.Render())
}

func TestSetScalarAppendsNewKey(t *testing.T) {
	f, err := Parse([]byte(sample))
	require.NoError(t, err)
	f.SetScalar("steamQueryPort", "27016", false)
	keys := f.Keys()
	require.NotEmpty(t, keys)
	require.Equal(t, "steamQueryPort", keys[len(keys)-1], "new key should be appended")
}

func TestSetArray(t *testing.T) {
	f := newFile()
	f.SetArray("motd", []Item{{Value: "a", Quoted: true}, {Value: "b", Quoted: true}})
	e, ok := f.Get("motd")
	require.True(t, ok)
	require.True(t, e.IsArray)
	assert.Equal(t, `{"a", "b"}`, e.Render())
}

func TestDiffFiles(t *testing.T) {
	oldFile, err := Parse([]byte(`hostname = "old"; maxPlayers = 40; disableVoN = 0;`))
	require.NoError(t, err)
	newFile, err := Parse([]byte(`hostname = "new"; maxPlayers = 40; steamQueryPort = 27016;`))
	require.NoError(t, err)

	d := DiffFiles(oldFile, newFile)
	require.False(t, d.Empty(), "expected a non-empty diff")
	assert.Equal(t, []string{"steamQueryPort"}, d.Added)
	assert.Equal(t, []string{"disableVoN"}, d.Removed)
	require.Len(t, d.Changed, 1)
	require.Equal(t, "hostname", d.Changed[0].Key)
	assert.Equal(t, `"old"`, d.Changed[0].Old)
	assert.Equal(t, `"new"`, d.Changed[0].New)
}

func TestDiffFilesEmptyWhenEqual(t *testing.T) {
	a, err := Parse([]byte(`hostname = "same";`))
	require.NoError(t, err)
	b, err := Parse([]byte(`hostname = "same";`))
	require.NoError(t, err)
	d := DiffFiles(a, b)
	require.True(t, d.Empty(), "expected an empty diff, got %+v", d)
}

func TestParseErrors(t *testing.T) {
	cases := []string{
		`= "no key";`,
		`hostname "missing equals";`,
		`hostname = "unterminated`,
		`beLoggerFilters[] = {"a", "b"`,
		`beLoggerFilters[] = "not an array";`,
		`hostname = ;`,
	}
	for _, c := range cases {
		_, err := Parse([]byte(c))
		assert.Error(t, err, "Parse(%q) should have failed", c)
	}
}

func TestParseEmpty(t *testing.T) {
	f, err := Parse([]byte("  \n// just a comment\n/* and a block */\n"))
	require.NoError(t, err)
	require.Empty(t, f.Keys())
}

func TestEntryStringFormatsArrayKey(t *testing.T) {
	f := newFile()
	f.SetArray("motd", []Item{{Value: "hi", Quoted: true}})
	e, _ := f.Get("motd")
	assert.True(t, strings.HasPrefix(e.String(), "motd[] = "), "String() = %q", e.String())
}

const withClass = `hostname = "x";
class Missions
{
    class DayZ
    {
        template="dayzOffline.chernarusplus"; // Mission to load
    };
};
maxPlayers = 60;
`

func TestClassBlocksParseAndRoundTrip(t *testing.T) {
	f, err := Parse([]byte(withClass))
	require.NoError(t, err)
	e, ok := f.GetPath([]string{"Missions", "DayZ"}, "template")
	require.True(t, ok)
	require.Equal(t, "dayzOffline.chernarusplus", e.Scalar.Value)
	m, ok := f.Get("Missions")
	assert.True(t, ok)
	assert.True(t, m.IsClass, "Missions must be a class entry")
	again, err := Parse(f.Bytes())
	require.NoError(t, err)
	require.Equal(t, string(f.Bytes()), string(again.Bytes()), "round trip")
	assert.Equal(t, []string{"hostname", "Missions", "maxPlayers"}, f.Keys(), "keys keep their order")
}

func TestSetPathScalarCreatesAndReplaces(t *testing.T) {
	f, _ := Parse([]byte(withClass))
	f.SetPathScalar([]string{"Missions", "DayZ"}, "template", "enoch", true)
	f.SetPathScalar([]string{"Other", "Deep"}, "k", "1", false)
	e, _ := f.GetPath([]string{"Missions", "DayZ"}, "template")
	assert.Equal(t, "enoch", e.Scalar.Value)
	e, ok := f.GetPath([]string{"Other", "Deep"}, "k")
	require.True(t, ok, "created path")
	assert.Equal(t, "1", e.Scalar.Value)
	_, ok = f.GetPath([]string{"Nope"}, "k")
	assert.False(t, ok, "a missing class has no entries")
	_, ok = f.GetPath([]string{"hostname"}, "k")
	assert.False(t, ok, "a scalar is not a class")
}

func TestDiffSeesInsideClasses(t *testing.T) {
	a, _ := Parse([]byte(withClass))
	b, _ := Parse([]byte(strings.Replace(withClass, "chernarusplus", "enoch", 1)))
	d := DiffFiles(a, b)
	require.Len(t, d.Changed, 1)
	assert.Equal(t, "Missions/DayZ/template", d.Changed[0].Key)
	assert.True(t, DiffFiles(a, a).Empty(), "the same file must not differ from itself")
}

func TestClassParseErrors(t *testing.T) {
	for _, bad := range []string{"class {};", "class A", "class A {", "class A { x = 1; }", "class A { x = ; };"} {
		_, err := Parse([]byte(bad))
		assert.Error(t, err, "%q must not parse", bad)
	}
}

func TestParseTrimsUnicodeSpaceAroundBareValues(t *testing.T) {
	f, err := Parse([]byte("maxPlayers = 60 ;\nmotd[] = { \"a\" , 2\f};\n"))
	require.NoError(t, err)
	e, ok := f.Get("maxPlayers")
	require.True(t, ok)
	require.Equal(t, "60", e.Scalar.Value)
}
