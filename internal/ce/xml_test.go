// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package ce

import (
	"strings"
	"testing"

	"github.com/beevik/etree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseXML(t testing.TB, s string) *etree.Document {
	t.Helper()
	doc := etree.NewDocument()
	err := doc.ReadFromString(s)
	require.NoError(t, err, "ReadFromString")
	return doc
}

func TestMergeXMLChildrenAppendsNew(t *testing.T) {
	base := parseXML(t, `<eventgroupdef><eventgroup name="A"/></eventgroupdef>`)
	overlay := parseXML(t, `<eventgroupdef><eventgroup name="B"/></eventgroupdef>`)

	err := MergeXMLChildren(base, overlay, "name")
	require.NoError(t, err, "MergeXMLChildren")

	names := elementNames(base.Root().ChildElements())
	require.Equal(t, []string{"A", "B"}, names)
}

func TestMergeXMLChildrenReplacesMatching(t *testing.T) {
	base := parseXML(t, `<eventgroupdef>
		<eventgroup name="A"><child>old</child></eventgroup>
		<eventgroup name="C"/>
	</eventgroupdef>`)
	overlay := parseXML(t, `<eventgroupdef><eventgroup name="A"><child>new</child></eventgroup></eventgroupdef>`)

	err := MergeXMLChildren(base, overlay, "name")
	require.NoError(t, err, "MergeXMLChildren")

	children := base.Root().ChildElements()
	require.Len(t, children, 2)
	require.Equal(t, []string{"A", "C"}, elementNames(children), "order changed")
	assert.Equal(t, "new", children[0].SelectElement("child").Text())
}

func TestMergeXMLChildrenWithoutMatchAttrAlwaysAppends(t *testing.T) {
	base := parseXML(t, `<mapgroupproto><group name="A"/></mapgroupproto>`)
	overlay := parseXML(t, `<mapgroupproto><group name="A"/></mapgroupproto>`)

	err := MergeXMLChildren(base, overlay, "")
	require.NoError(t, err, "MergeXMLChildren")
	require.Len(t, base.Root().ChildElements(), 2)
}

func TestMergeXMLChildrenNoRootErrors(t *testing.T) {
	base := etree.NewDocument()
	overlay := parseXML(t, `<a/>`)
	err := MergeXMLChildren(base, overlay, "name")
	require.Error(t, err, "expected an error for a base document with no root")

	overlay2 := etree.NewDocument()
	base2 := parseXML(t, `<a/>`)
	err = MergeXMLChildren(base2, overlay2, "name")
	require.Error(t, err, "expected an error for an overlay document with no root")
}

func TestAppendCEFolderCreatesRootAndFiles(t *testing.T) {
	doc := etree.NewDocument()
	err := AppendCEFolder(doc, "mod_123", []CEFile{
		{Name: "types.xml", Type: "types"},
		{Name: "events.xml", Type: "events"},
	})
	require.NoError(t, err, "AppendCEFolder")

	root := doc.Root()
	require.NotNil(t, root)
	require.Equal(t, "economycore", root.Tag)
	ces := root.SelectElements("ce")
	require.Len(t, ces, 1)
	assert.Equal(t, "mod_123", ces[0].SelectAttrValue("folder", ""))
	files := ces[0].SelectElements("file")
	require.Len(t, files, 2)
	require.Equal(t, "types.xml", files[0].SelectAttrValue("name", ""))
}

func TestAppendCEFolderIsOrderedAndDeterministic(t *testing.T) {
	doc := etree.NewDocument()
	err := AppendCEFolder(doc, "mod_1", nil)
	require.NoError(t, err, "AppendCEFolder")
	err = AppendCEFolder(doc, "mod_2", nil)
	require.NoError(t, err, "AppendCEFolder")
	folders := []string{}
	for _, ce := range doc.Root().SelectElements("ce") {
		folders = append(folders, ce.SelectAttrValue("folder", ""))
	}
	require.Equal(t, []string{"mod_1", "mod_2"}, folders)
}

func TestAppendCEFolderRejectsWrongRoot(t *testing.T) {
	doc := parseXML(t, `<notEconomy/>`)
	err := AppendCEFolder(doc, "mod_1", nil)
	require.Error(t, err, "expected an error for a non-<economycore> root")
}

func elementNames(els []*etree.Element) []string {
	names := make([]string, len(els))
	for i, e := range els {
		if n := e.SelectAttrValue("name", ""); n != "" {
			names[i] = n
		} else {
			names[i] = e.Tag
		}
	}
	return names
}

func TestMergeXMLChildrenIgnoresBlankMatchAttrValue(t *testing.T) {
	base := parseXML(t, `<a><item name=""/></a>`)
	overlay := parseXML(t, `<a><item name=""/></a>`)
	err := MergeXMLChildren(base, overlay, "name")
	require.NoError(t, err, "MergeXMLChildren")
	// Both items have an empty match key, so they cannot be matched against
	// each other and must both be present (appended, not merged).
	require.Len(t, base.Root().ChildElements(), 2)
}

func TestMergeXMLChildrenByNameAndPosition(t *testing.T) {
	parse := func(s string) *etree.Document {
		d := etree.NewDocument()
		err := d.ReadFromString(s)
		require.NoError(t, err)
		return d
	}
	base := parse(`<map><group name="A" pos="1"/><group name="A" pos="2"/><group name="B" pos="3"/></map>`)
	over := parse(`<map><group name="A" pos="2" x="new"/><group name="A" pos="9"/><group name="C"/></map>`)
	err := MergeXMLChildren(base, over, "name,pos")
	require.NoError(t, err)
	out, _ := base.WriteToString()
	// the position-2 group is replaced in place, the one at 9 is added, C has no pos and is added
	for _, want := range []string{`pos="1"/>`, `pos="2" x="new"`, `pos="9"`, `name="C"`, `name="B"`} {
		assert.Contains(t, out, want)
	}
	assert.Equal(t, 5, strings.Count(out, "<group"))
}
