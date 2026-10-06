// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package ce

import (
	"fmt"
	"strings"

	"github.com/beevik/etree"
)

// MergeXMLChildren merges overlay's root element children into base's root
// element (in place): a child is matched against base's existing children
// by tag name plus the value of matchAttr (when matchAttr is non-empty and
// present on both sides; "name,pos" means all the attributes listed, for
// elements like the groups of mapgrouppos.xml, which share a name and differ
// in their position); a match replaces the base child in its original
// position, and anything unmatched is appended, in overlay's order, after
// base's existing children. This is the generic strategy behind
// mapgroup*/events*/env/randompresets merges (FR-09); per-file-type quirks
// are layered on top once spike S3 has verified fixtures (see the package
// doc comment).
func MergeXMLChildren(base, overlay *etree.Document, matchAttr string) error {
	baseRoot := base.Root()
	overlayRoot := overlay.Root()
	if baseRoot == nil {
		return fmt.Errorf("ce: base document has no root element")
	}
	if overlayRoot == nil {
		return fmt.Errorf("ce: overlay document has no root element")
	}

	attrs := strings.Split(matchAttr, ",")
	for _, child := range overlayRoot.ChildElements() {
		if matchAttr != "" {
			if key, ok := matchKey(child, attrs); ok {
				if existing := findChildByKey(baseRoot, child.Tag, attrs, key); existing != nil {
					baseRoot.InsertChildAt(existing.Index(), child.Copy())
					baseRoot.RemoveChild(existing)
					continue
				}
			}
		}
		baseRoot.AddChild(child.Copy())
	}
	return nil
}

// matchKey is the identity of an element: the values of attrs, which must all be present.
func matchKey(e *etree.Element, attrs []string) (string, bool) {
	vals := make([]string, len(attrs))
	for i, a := range attrs {
		v := e.SelectAttrValue(a, "")
		if v == "" {
			return "", false
		}
		vals[i] = v
	}
	return strings.Join(vals, "\x00"), true
}

func findChildByKey(parent *etree.Element, tag string, attrs []string, key string) *etree.Element {
	for _, e := range parent.ChildElements() {
		if k, ok := matchKey(e, attrs); e.Tag == tag && ok && k == key {
			return e
		}
	}
	return nil
}

// CEFile describes one <file> entry inside a <ce folder="..."> block of
// cfgeconomycore.xml.
type CEFile struct {
	Name string
	Type string
}

// AppendCEFolder registers a new <ce folder="folder"> block (with the given
// files, in order) at the end of cfgeconomycore.xml's <economycore> root,
// creating the root if the document is empty. Registration order must be
// deterministic (A8#1): callers drive that by calling this once per
// mod/overlay in a fixed, documented order.
func AppendCEFolder(doc *etree.Document, folder string, files []CEFile) error {
	root := doc.Root()
	if root == nil {
		root = doc.CreateElement("economycore")
	}
	if root.Tag != "economycore" {
		return fmt.Errorf("ce: cfgeconomycore.xml root is <%s>, want <economycore>", root.Tag)
	}

	ce := root.CreateElement("ce")
	ce.CreateAttr("folder", folder)
	for _, f := range files {
		file := ce.CreateElement("file")
		file.CreateAttr("name", f.Name)
		file.CreateAttr("type", f.Type)
	}
	return nil
}
