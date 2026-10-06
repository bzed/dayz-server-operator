// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package integrate

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Normalizers are the names `normalize:` of an integration takes. They repair the quirks
// mod authors' files are known for; the legacy installxml did the same.
const (
	// NormEventPosDefRoot renames the root <events> of cfgeventspawns.xml to <eventposdef>.
	NormEventPosDefRoot = "eventposdef-root"
	// NormWrapRoot wraps a file that is a bare list of elements into the root element of its kind.
	NormWrapRoot = "wrap-root"
	// NormXMLDecl adds the XML declaration when the file has none.
	NormXMLDecl = "xml-decl"
)

// KnownNormalizer reports whether name is a normalizer.
func KnownNormalizer(name string) bool {
	switch name {
	case NormEventPosDefRoot, NormWrapRoot, NormXMLDecl:
		return true
	}
	return false
}

// rootOf is the root element of each central economy XML file.
var rootOf = map[string]string{
	"types.xml":                   "types",
	"events.xml":                  "events",
	"cfgspawnabletypes.xml":       "spawnabletypes",
	"cfgeventspawns.xml":          "eventposdef",
	"cfgeventgroups.xml":          "eventgroupdef",
	"cfgrandompresets.xml":        "randompresets",
	"cfgignorelist.xml":           "ignore",
	"cfglimitsdefinition.xml":     "lists",
	"cfglimitsdefinitionuser.xml": "user_lists",
	"cfgenvironment.xml":          "env",
	"mapgroupproto.xml":           "prototype",
	"mapgrouppos.xml":             "map",
	"mapgroupcluster.xml":         "map",
	"mapgroupdirt.xml":            "map",
	"mapclusterproto.xml":         "map",
	"cfgweather.xml":              "weather",
	"messages.xml":                "messages",
	"globals.xml":                 "variables",
	"economy.xml":                 "economy",
}

const xmlDecl = `<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>`

// Normalize applies the named normalizers, in the order given, to one file's content. A file that
// is not XML (a .json) is returned as it is.
func Normalize(file string, data []byte, names []string) ([]byte, error) {
	if !strings.HasSuffix(strings.ToLower(file), ".xml") || len(names) == 0 {
		return data, nil
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	for _, n := range names {
		var err error
		switch n {
		case NormEventPosDefRoot:
			data = renameRoot(file, data)
		case NormWrapRoot:
			data, err = wrapRoot(file, data)
		case NormXMLDecl:
			data = addDecl(data)
		default:
			err = fmt.Errorf("unknown normalizer %q", n)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", file, n, err)
		}
	}
	return data, nil
}

func addDecl(data []byte) []byte {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("<?xml")) {
		return data
	}
	return append([]byte(xmlDecl+"\n"), bytes.TrimLeft(data, "\r\n\t ")...)
}

// body splits an XML declaration off the front.
func body(data []byte) (decl, rest []byte) {
	t := bytes.TrimLeft(data, "\r\n\t ")
	if bytes.HasPrefix(t, []byte("<?xml")) {
		if i := bytes.Index(t, []byte("?>")); i >= 0 {
			return t[:i+2], t[i+2:]
		}
	}
	return nil, data
}

// topLevel counts the elements at depth 0 and returns the name of the first.
func topLevel(data []byte) (n int, first string, err error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	depth := 0
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			return n, first, nil
		}
		if err != nil {
			return n, first, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				if n == 0 {
					first = t.Name.Local
				}
				n++
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
}

func renameRoot(file string, data []byte) []byte {
	if !strings.EqualFold(baseName(file), "cfgeventspawns.xml") {
		return data
	}
	decl, rest := body(data)
	if _, first, err := topLevel(rest); err != nil || first != "events" {
		return data
	}
	i := bytes.Index(rest, []byte("<events"))
	j := bytes.LastIndex(rest, []byte("</events>"))
	if i < 0 || j < i {
		return data
	}
	var b bytes.Buffer
	b.Write(decl)
	b.Write(rest[:i])
	b.WriteString("<eventposdef")
	b.Write(rest[i+len("<events") : j])
	b.WriteString("</eventposdef>")
	b.Write(rest[j+len("</events>"):])
	return b.Bytes()
}

func wrapRoot(file string, data []byte) ([]byte, error) {
	decl, rest := body(data)
	n, _, err := topLevel(rest)
	if err != nil {
		return nil, err
	}
	if n <= 1 {
		return data, nil
	}
	root, ok := rootOf[strings.ToLower(baseName(file))]
	if !ok {
		return nil, fmt.Errorf("no root element is known for this file name")
	}
	var b bytes.Buffer
	b.Write(decl)
	b.WriteString("<" + root + ">\n")
	b.Write(bytes.TrimSpace(rest))
	b.WriteString("\n</" + root + ">\n")
	return b.Bytes(), nil
}

func baseName(file string) string {
	if i := strings.LastIndexAny(file, `/\`); i >= 0 {
		return file[i+1:]
	}
	return file
}
