// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package mission

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/beevik/etree"
)

// canonChildren lists the children of the root as "tag name=<name>" -> canonical content (spaces
// and line breaks between elements removed), in document order.
func canonChildren(t *testing.T, data []byte) ([]string, map[string]string) {
	t.Helper()
	doc := etree.NewDocument()
	doc.ReadSettings.PreserveCData = true
	if err := doc.ReadFromBytes(data); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var keys []string
	content := map[string]string{}
	for _, c := range doc.Root().ChildElements() {
		d := etree.NewDocument()
		d.SetRoot(c.Copy())
		d.Indent(etree.NoIndent)
		b, _ := d.WriteToBytes()
		key := c.Tag + " " + c.SelectAttrValue("name", "")
		keys = append(keys, key)
		content[key] = strings.Join(strings.Fields(string(b)), " ")
	}
	return keys, content
}

// TestAgainstXmlmergeOracle is spike S3: it merges real mod files into the real Central Economy
// files with the legacy tool (xmlmerge of gwenhywfar) and with MergeXMLFile, and compares what
// ends up in the file. It runs only with DZO_S3_MODS (the legacy repository's files/mods) and
// DZO_S3_CE (a checkout of Bohemia's Central Economy repository), and needs xmlmerge installed.
func TestAgainstXmlmergeOracle(t *testing.T) {
	mods, ce := os.Getenv("DZO_S3_MODS"), os.Getenv("DZO_S3_CE")
	if mods == "" || ce == "" {
		t.Skip("set DZO_S3_MODS and DZO_S3_CE to compare with xmlmerge")
	}
	if _, err := exec.LookPath("xmlmerge"); err != nil {
		t.Skip("xmlmerge is not installed")
	}
	cases := []struct{ mod, file, root string }{
		{"3115714092", "cfgeventspawns.xml", "eventposdef"},
		{"3115714092", "cfgeventgroups.xml", "eventgroupdef"},
		{"2950280649", "cfgenvironment.xml", "env"},
		{"3336530310", "cfgrandompresets.xml", "randompresets"},
		{"2971190303", "cfgeventspawns.xml", "eventposdef"}, // a fragment without a root element
	}
	for _, c := range cases {
		t.Run(c.mod+"/"+c.file, func(t *testing.T) {
			modFile := filepath.Join(mods, c.mod, c.file)
			base := filepath.Join(ce, "dayzOffline.chernarusplus", c.file)
			if _, err := os.Stat(modFile); err != nil {
				t.Skip(err)
			}
			modData, err := os.ReadFile(modFile) //nolint:gosec // an operator-supplied fixture
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "legacy.xml")
			cmd := exec.Command("xmlmerge", "-o", out, modFile, base) // the legacy order: the mod file first
			if o, err := cmd.CombinedOutput(); err != nil {
				t.Logf("xmlmerge failed on %s/%s: %v (%s)", c.mod, c.file, err, strings.TrimSpace(string(o)))
			}
			staging := t.TempDir()
			baseData, _ := os.ReadFile(base) //nolint:gosec // an operator-supplied fixture
			if err := etree.NewDocument().ReadFromBytes(baseData); err != nil {
				t.Skipf("the Central Economy file itself is not well-formed XML (%v)", err)
			}
			if err := WriteFile(staging, c.file, baseData); err != nil {
				t.Fatal(err)
			}
			fragment := !strings.Contains(string(modData), "<"+c.root)
			overlay := modData
			if fragment { // no root element: wrap it, as a normalisation would
				overlay = []byte("<" + c.root + ">" + string(modData) + "</" + c.root + ">")
			}
			if err := MergeXMLFile(staging, c.file, c.root, overlay, "name"); err != nil {
				t.Fatal(err)
			}
			ours, _ := os.ReadFile(filepath.Join(staging, c.file)) //nolint:gosec // our own output
			oursKeys, oursContent := canonChildren(t, ours)
			legacy, err := os.ReadFile(out) //nolint:gosec // the oracle's output
			if err != nil || fragment {
				t.Logf("xmlmerge gives nothing usable for this file (fragment=%v): it takes the first element for the root", fragment)
				return
			}
			if err := etree.NewDocument().ReadFromBytes(legacy); err != nil {
				t.Logf("xmlmerge wrote XML that does not parse (%v): the legacy start would have stopped here", err)
				return
			}
			legacyKeys, legacyContent := canonChildren(t, legacy)
			set := func(l []string) string {
				m := map[string]bool{}
				for _, k := range l {
					m[k] = true
				}
				var out []string
				for k := range m {
					out = append(out, k)
				}
				sort.Strings(out)
				return strings.Join(out, "\n")
			}
			t.Logf("children: ours %d, xmlmerge %d (it keeps duplicates, we keep the last one by name)", len(oursKeys), len(legacyKeys))
			if set(oursKeys) != set(legacyKeys) {
				t.Errorf("the set of children differs\nours:     %v\nxmlmerge: %v", oursKeys, legacyKeys)
			}
			// What the mod contributes arrives unchanged (the last definition of a name wins).
			_, modContent := canonChildren(t, overlay)
			for k, v := range modContent {
				if strings.HasSuffix(k, " ") {
					continue // unnamed elements (territories, ...) cannot be told apart by key
				}
				if oursContent[k] != v {
					t.Errorf("mod child %q was changed by the merge:\nours: %.200s\nmod:  %.200s", k, oursContent[k], v)
				}
			}
			_ = legacyContent
		})
	}
}
