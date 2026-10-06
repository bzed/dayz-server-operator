// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package ce

import (
	"strings"
	"testing"

	"github.com/beevik/etree"
)

func parseXML(t *testing.T, s string) *etree.Document {
	t.Helper()
	doc := etree.NewDocument()
	if err := doc.ReadFromString(s); err != nil {
		t.Fatalf("ReadFromString: %v", err)
	}
	return doc
}

func TestMergeXMLChildrenAppendsNew(t *testing.T) {
	base := parseXML(t, `<eventgroupdef><eventgroup name="A"/></eventgroupdef>`)
	overlay := parseXML(t, `<eventgroupdef><eventgroup name="B"/></eventgroupdef>`)

	if err := MergeXMLChildren(base, overlay, "name"); err != nil {
		t.Fatalf("MergeXMLChildren: %v", err)
	}

	names := elementNames(base.Root().ChildElements())
	if !equal(names, []string{"A", "B"}) {
		t.Fatalf("names = %v, want [A B]", names)
	}
}

func TestMergeXMLChildrenReplacesMatching(t *testing.T) {
	base := parseXML(t, `<eventgroupdef>
		<eventgroup name="A"><child>old</child></eventgroup>
		<eventgroup name="C"/>
	</eventgroupdef>`)
	overlay := parseXML(t, `<eventgroupdef><eventgroup name="A"><child>new</child></eventgroup></eventgroupdef>`)

	if err := MergeXMLChildren(base, overlay, "name"); err != nil {
		t.Fatalf("MergeXMLChildren: %v", err)
	}

	children := base.Root().ChildElements()
	if len(children) != 2 {
		t.Fatalf("expected 2 children (A replaced in place, C untouched), got %d", len(children))
	}
	if children[0].SelectAttrValue("name", "") != "A" || children[1].SelectAttrValue("name", "") != "C" {
		t.Fatalf("order changed: %v", elementNames(children))
	}
	if children[0].SelectElement("child").Text() != "new" {
		t.Errorf("A's child = %q, want new", children[0].SelectElement("child").Text())
	}
}

func TestMergeXMLChildrenWithoutMatchAttrAlwaysAppends(t *testing.T) {
	base := parseXML(t, `<mapgroupproto><group name="A"/></mapgroupproto>`)
	overlay := parseXML(t, `<mapgroupproto><group name="A"/></mapgroupproto>`)

	if err := MergeXMLChildren(base, overlay, ""); err != nil {
		t.Fatalf("MergeXMLChildren: %v", err)
	}
	if len(base.Root().ChildElements()) != 2 {
		t.Fatalf("expected both <group name=A> entries to coexist when matchAttr is empty")
	}
}

func TestMergeXMLChildrenNoRootErrors(t *testing.T) {
	base := etree.NewDocument()
	overlay := parseXML(t, `<a/>`)
	if err := MergeXMLChildren(base, overlay, "name"); err == nil {
		t.Fatal("expected an error for a base document with no root")
	}

	overlay2 := etree.NewDocument()
	base2 := parseXML(t, `<a/>`)
	if err := MergeXMLChildren(base2, overlay2, "name"); err == nil {
		t.Fatal("expected an error for an overlay document with no root")
	}
}

func TestAppendCEFolderCreatesRootAndFiles(t *testing.T) {
	doc := etree.NewDocument()
	if err := AppendCEFolder(doc, "mod_123", []CEFile{
		{Name: "types.xml", Type: "types"},
		{Name: "events.xml", Type: "events"},
	}); err != nil {
		t.Fatalf("AppendCEFolder: %v", err)
	}

	root := doc.Root()
	if root == nil || root.Tag != "economycore" {
		t.Fatalf("root = %+v, want <economycore>", root)
	}
	ces := root.SelectElements("ce")
	if len(ces) != 1 {
		t.Fatalf("expected 1 <ce> element, got %d", len(ces))
	}
	if ces[0].SelectAttrValue("folder", "") != "mod_123" {
		t.Errorf("folder = %q", ces[0].SelectAttrValue("folder", ""))
	}
	files := ces[0].SelectElements("file")
	if len(files) != 2 || files[0].SelectAttrValue("name", "") != "types.xml" {
		t.Fatalf("files = %+v", files)
	}
}

func TestAppendCEFolderIsOrderedAndDeterministic(t *testing.T) {
	doc := etree.NewDocument()
	if err := AppendCEFolder(doc, "mod_1", nil); err != nil {
		t.Fatalf("AppendCEFolder: %v", err)
	}
	if err := AppendCEFolder(doc, "mod_2", nil); err != nil {
		t.Fatalf("AppendCEFolder: %v", err)
	}
	folders := []string{}
	for _, ce := range doc.Root().SelectElements("ce") {
		folders = append(folders, ce.SelectAttrValue("folder", ""))
	}
	if !equal(folders, []string{"mod_1", "mod_2"}) {
		t.Fatalf("folders = %v, want [mod_1 mod_2]", folders)
	}
}

func TestAppendCEFolderRejectsWrongRoot(t *testing.T) {
	doc := parseXML(t, `<notEconomy/>`)
	if err := AppendCEFolder(doc, "mod_1", nil); err == nil {
		t.Fatal("expected an error for a non-<economycore> root")
	}
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

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMergeXMLChildrenIgnoresBlankMatchAttrValue(t *testing.T) {
	base := parseXML(t, `<a><item name=""/></a>`)
	overlay := parseXML(t, `<a><item name=""/></a>`)
	if err := MergeXMLChildren(base, overlay, "name"); err != nil {
		t.Fatalf("MergeXMLChildren: %v", err)
	}
	// Both items have an empty match key, so they cannot be matched against
	// each other and must both be present (appended, not merged).
	if len(base.Root().ChildElements()) != 2 {
		t.Fatalf("expected 2 children, got %d", len(base.Root().ChildElements()))
	}
}

func TestMergeXMLChildrenByNameAndPosition(t *testing.T) {
	parse := func(s string) *etree.Document {
		d := etree.NewDocument()
		if err := d.ReadFromString(s); err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := parse(`<map><group name="A" pos="1"/><group name="A" pos="2"/><group name="B" pos="3"/></map>`)
	over := parse(`<map><group name="A" pos="2" x="new"/><group name="A" pos="9"/><group name="C"/></map>`)
	if err := MergeXMLChildren(base, over, "name,pos"); err != nil {
		t.Fatal(err)
	}
	out, _ := base.WriteToString()
	// the position-2 group is replaced in place, the one at 9 is added, C has no pos and is added
	for _, want := range []string{`pos="1"/>`, `pos="2" x="new"`, `pos="9"`, `name="C"`, `name="B"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	if strings.Count(out, "<group") != 5 {
		t.Errorf("5 groups expected: %s", out)
	}
}
