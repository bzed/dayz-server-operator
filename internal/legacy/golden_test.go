// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package legacy

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bzed/dayz-server-operator/internal/integrate"
	"github.com/bzed/dayz-server-operator/internal/mission"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// TestGoldenAgainstLegacyRender is spike S0: the mission dzo builds for a converted branch against the
// mission the legacy mergexml built for it (scripts/s0/legacy-render.sh). The two are not byte-equal by design
// (the legacy merge concatenates, dzo replaces by name; JSON lists are appended and de-duplicated; the
// formatting differs), so the files are compared as data and the differences are listed. It runs when
//
//	DZO_S0_LEGACY   the output directory of legacy-render.sh (mission/, map)
//	DZO_S0_SITE     the site converted from the same branch (dzo legacy convert-config)
//	DZO_S0_INSTANCE the instance (branch) name
//	DZO_S0_CE       the Central Economy repository the legacy render used
//
// are set. DZO_S0_REPORT names a file the report is written to.
func TestGoldenAgainstLegacyRender(t *testing.T) {
	legacyDir, siteDir, name, ce := os.Getenv("DZO_S0_LEGACY"), os.Getenv("DZO_S0_SITE"), os.Getenv("DZO_S0_INSTANCE"), os.Getenv("DZO_S0_CE")
	if legacyDir == "" || siteDir == "" || name == "" || ce == "" {
		t.Skip("DZO_S0_LEGACY, DZO_S0_SITE, DZO_S0_INSTANCE and DZO_S0_CE are not set")
	}
	mapName, err := os.ReadFile(filepath.Join(legacyDir, "map"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := site.LoadTree(siteDir)
	if err != nil {
		t.Fatal(err)
	}
	// A "source: mod" file lives inside a workshop mod that is not downloaded here: the legacy render could
	// not use it either (xml.sh printed "cannot stat"), so leave those files out of both.
	dropModFiles := func(m map[uint64]site.LoadedIntegration) {
		for _, li := range m {
			for k, f := range li.Files {
				if f.Source == "mod" {
					delete(li.Files, k)
				}
			}
		}
	}
	dropModFiles(tree.Integrations)
	for _, m := range tree.InstanceIntegrations {
		dropModFiles(m)
	}
	raw := tree.Instances[name]
	inst := &resolve.Instance{Name: name, Map: strings.TrimSpace(string(mapName)), Overlays: raw.Overlays}
	inst.Paths.Live = t.TempDir()
	for _, m := range raw.Mods {
		inst.Mods = append(inst.Mods, resolve.Mod{ID: m.ID, Dir: t.TempDir(), Server: m.Server})
	}
	var skipped []string
	cs, err := integrate.Collect(context.Background(), tree, inst, integrate.Options{
		CacheDir: t.TempDir(),
		Log:      func(f string, a ...any) { skipped = append(skipped, fmt.Sprintf(f, a...)) },
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for i := range cs {
		cs[i].AfterMerge = nil // the hooks need xmlstarlet; the legacy run did them in its container
	}
	staging, err := mission.BuildStaging(mission.RenderInput{
		PristineDir: filepath.Join(ce, inst.Map), FallbackDir: filepath.Join(ce, "dayzOffline.chernarusplus"), Contributions: cs,
	})
	if err != nil {
		t.Fatalf("dzo render: %v", err)
	}
	defer os.RemoveAll(staging) //nolint:errcheck // scratch

	var rep strings.Builder
	diffs := compareTrees(&rep, filepath.Join(legacyDir, "mission"), staging)
	fmt.Fprintf(&rep, "\n%d file(s) differ as data\n", diffs)
	if p := os.Getenv("DZO_S0_REPORT"); p != "" {
		if err := os.WriteFile(p, []byte(rep.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("\n" + rep.String())
}

func walk(root string) map[string]bool {
	out := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			out[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	return out
}

func compareTrees(w *strings.Builder, legacyRoot, dzoRoot string) int {
	l, d := walk(legacyRoot), walk(dzoRoot)
	var names []string
	for n := range l {
		names = append(names, n)
	}
	for n := range d {
		if !l[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	diffs := 0
	for _, n := range names {
		switch {
		case !d[n]:
			fmt.Fprintf(w, "ONLY LEGACY  %s\n", n)
			diffs++
		case !l[n]:
			fmt.Fprintf(w, "ONLY DZO     %s\n", n)
			diffs++
		default:
			a, _ := os.ReadFile(filepath.Join(legacyRoot, n)) //nolint:gosec // test
			b, _ := os.ReadFile(filepath.Join(dzoRoot, n))    //nolint:gosec // test
			if bytes.Equal(a, b) {
				continue
			}
			if msg := compareData(n, a, b); msg != "" {
				fmt.Fprintf(w, "DIFFERENT    %s: %s\n", n, msg)
				diffs++
			} else {
				fmt.Fprintf(w, "same data    %s (the bytes differ)\n", n)
			}
		}
	}
	return diffs
}

// compareData compares a file as data: "" if the data is the same.
func compareData(name string, a, b []byte) string {
	switch {
	case strings.HasSuffix(name, ".xml"):
		ea, err1 := canonChildren(a)
		eb, err2 := canonChildren(b)
		if err1 != nil || err2 != nil {
			return fmt.Sprintf("does not parse (legacy: %v, dzo: %v)", err1, err2)
		}
		return diffSets(ea, eb)
	case strings.HasSuffix(name, ".json"):
		var ja, jb any
		if json.Unmarshal(a, &ja) != nil || json.Unmarshal(b, &jb) != nil {
			return "does not parse as JSON"
		}
		if reflect.DeepEqual(ja, jb) {
			return ""
		}
		return "JSON differs: " + jsonDiff("", ja, jb)
	}
	return fmt.Sprintf("%d bytes against %d bytes", len(a), len(b))
}

func jsonDiff(path string, a, b any) string {
	ma, oka := a.(map[string]any)
	mb, okb := b.(map[string]any)
	if oka && okb {
		var keys []string
		for k := range ma {
			keys = append(keys, k)
		}
		for k := range mb {
			if _, ok := ma[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			if reflect.DeepEqual(ma[k], mb[k]) {
				continue
			}
			out = append(out, jsonDiff(path+"/"+k, ma[k], mb[k]))
		}
		return strings.Join(out, "; ")
	}
	aa, oka := a.([]any)
	ab, okb := b.([]any)
	if oka && okb {
		return fmt.Sprintf("%s: list of %d against %d", path, len(aa), len(ab))
	}
	return path + " differs"
}

// canonChildren returns the canonical form of every child of the root element: attributes sorted, text
// trimmed, descendants in document order.
func canonChildren(data []byte) ([]string, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	var out []string
	var stack []*strings.Builder
	depth := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				continue
			}
			var b strings.Builder
			attrs := t.Attr
			sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name.Local < attrs[j].Name.Local })
			b.WriteString("<" + t.Name.Local)
			for _, a := range attrs {
				fmt.Fprintf(&b, " %s=%q", a.Name.Local, a.Value)
			}
			b.WriteString(">")
			stack = append(stack, &b)
		case xml.CharData:
			if s := strings.TrimSpace(string(t)); s != "" && len(stack) > 0 {
				stack[len(stack)-1].WriteString(s)
			}
		case xml.EndElement:
			if depth > 1 {
				b := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				b.WriteString("</" + t.Name.Local + ">")
				if depth == 2 {
					out = append(out, b.String())
				} else {
					stack[len(stack)-1].WriteString(b.String())
				}
			}
			depth--
		}
	}
}

func diffSets(a, b []string) string {
	count := func(l []string) map[string]int {
		m := map[string]int{}
		for _, s := range l {
			m[s]++
		}
		return m
	}
	ca, cb := count(a), count(b)
	var onlyA, onlyB []string
	for s, n := range ca {
		if cb[s] < n {
			onlyA = append(onlyA, s)
		}
	}
	for s, n := range cb {
		if ca[s] < n {
			onlyB = append(onlyB, s)
		}
	}
	if len(onlyA) == 0 && len(onlyB) == 0 {
		return ""
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	short := func(l []string) string {
		var names []string
		for _, s := range l {
			n := s
			if i := strings.Index(s, ">"); i > 0 {
				n = s[:i+1]
			}
			if len(n) > 70 {
				n = n[:70] + "…"
			}
			names = append(names, n)
		}
		if len(names) > 4 {
			names = append(names[:4], fmt.Sprintf("… %d more", len(names)-4))
		}
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%d element(s) only in legacy (%s); %d only in dzo (%s); legacy %d, dzo %d elements", len(onlyA), short(onlyA), len(onlyB), short(onlyB), len(a), len(b))
}
