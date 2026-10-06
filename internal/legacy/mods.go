// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package legacy

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// modInfo is what a branch says about one workshop mod.
type modInfo struct {
	id      uint64
	name    string
	aliases []uint64
}

var (
	atLink   = regexp.MustCompile(`^files/mods/@([^/]+)$`)
	idLink   = regexp.MustCompile(`^files/mods/(\d+)$`)
	idFile   = regexp.MustCompile(`^files/mods/(\d+)/([^/]+)$`)
	idAtLink = regexp.MustCompile(`^files/mods/(\d+)/@([^/]+)$`)
	envLine  = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
)

func parseID(s string) (uint64, bool) {
	id, err := strconv.ParseUint(path.Base(strings.TrimSpace(s)), 10, 64)
	return id, err == nil && id != 0
}

// readMods collects the mod names (files/mods/@Name links to the id directory) and the aliases
// (files/mods/<id> links to another id).
func readMods(b *branch) map[uint64]*modInfo {
	mods := map[uint64]*modInfo{}
	get := func(id uint64) *modInfo {
		if mods[id] == nil {
			mods[id] = &modInfo{id: id}
		}
		return mods[id]
	}
	for _, e := range b.list {
		switch {
		case atLink.MatchString(e.Path) && e.Mode == "120000":
			if id, ok := parseID(string(b.show(e.Path))); ok {
				get(id).name = atLink.FindStringSubmatch(e.Path)[1]
			}
		case idLink.MatchString(e.Path) && e.Mode == "120000":
			alias, _ := parseID(idLink.FindStringSubmatch(e.Path)[1])
			if id, ok := parseID(string(b.show(e.Path))); ok && alias != 0 {
				m := get(id)
				m.aliases = append(m.aliases, alias)
			}
		}
	}
	for _, e := range b.list {
		if m := idAtLink.FindStringSubmatch(e.Path); m != nil && e.Mode == "120000" {
			id, _ := strconv.ParseUint(m[1], 10, 64)
			if mi := get(id); mi.name == "" {
				mi.name = m[2]
			}
		}
	}
	for _, m := range mods {
		sort.Slice(m.aliases, func(i, j int) bool { return m.aliases[i] < m.aliases[j] })
	}
	return mods
}

// parseEnv reads the KEY=value lines of a shell file; `${VAR}` of earlier lines is substituted.
func parseEnv(data []byte) map[string]string {
	env := map[string]string{}
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		m := envLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[2])
		if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
			if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
				v = v[1 : 1+end]
			}
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		for k, x := range env {
			v = strings.ReplaceAll(v, "${"+k+"}", x)
		}
		env[m[1]] = v
	}
	return env
}

// matchesMap says whether the suffix of an init.c.<name> file names the template of a branch.
func matchesMap(suffix, template string) bool {
	return template == suffix || strings.HasSuffix(template, "."+suffix) || strings.Contains(template, suffix)
}

func yamlQuote(s string) string { return strconv.Quote(s) }

// buildIntegrations converts the files/mods/<id>/ directories of a branch.
func buildIntegrations(b *branch, templates []string, rep *report) map[uint64]integration {
	byID := map[uint64]map[string]Entry{}
	for _, e := range b.list {
		m := idFile.FindStringSubmatch(e.Path)
		if m == nil || e.Mode == "120000" {
			continue
		}
		id, _ := strconv.ParseUint(m[1], 10, 64)
		if byID[id] == nil {
			byID[id] = map[string]Entry{}
		}
		byID[id][m[2]] = e
	}
	out := map[uint64]integration{}
	var ids []uint64
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		entries := byID[id]
		dir := fmt.Sprintf("files/mods/%d/", id)
		env := parseEnv(b.show(dir + "xml.env"))
		files := map[string][]byte{}
		var lines []string
		var hasXML bool
		used := map[string]bool{"xml.env": true, "map.env": true, "README.md": true, "start.sh": true}
		name := fmt.Sprintf("mod%d", id)
		if m := b.mods[id]; m != nil && m.name != "" {
			name = m.name
		}
		for _, f := range xmlEnvFiles {
			key := strings.ToUpper(strings.SplitN(f, ".", 2)[0])
			val := env[key]
			switch {
			case strings.HasPrefix(val, "http"):
				lines = append(lines, fmt.Sprintf("  %s: {source: url, url: %s}", f, yamlQuote(val)))
			case strings.HasPrefix(val, "local"):
				if _, ok := entries[f]; ok {
					files["files/"+f] = b.show(dir + f)
					used[f] = true
					lines = append(lines, fmt.Sprintf("  %s: {source: local, path: files/%s}", f, f))
					break
				}
				if f == "init.c" {
					var variants []string
					for n := range entries {
						if strings.HasPrefix(n, "init.c.") {
							variants = append(variants, n)
						}
					}
					sort.Strings(variants)
					if len(variants) > 0 {
						v := variants[0]
						suffix := strings.TrimPrefix(v, "init.c.")
						var maps []string
						for _, t := range templates {
							if matchesMap(suffix, t) {
								maps = append(maps, t)
							}
						}
						if len(maps) == 0 {
							maps = []string{suffix}
						}
						files["files/"+v] = b.show(dir + v)
						for _, x := range variants {
							used[x] = true
						}
						lines = append(lines, fmt.Sprintf("  init.c: {source: local, path: files/%s, maps: [%s]}", v, strings.Join(maps, ", ")))
						rep.add(b.name, "mod %d (%s): init.c comes from %s, a unified diff for the map(s) %s; the legacy start asked for a file called init.c, which does not exist (a legacy quirk), so this patch was never applied there", id, name, v, strings.Join(maps, ", "))
						if len(variants) > 1 {
							rep.add(b.name, "mod %d (%s): more than one init.c variant (%s); only %s was converted, decide which maps need the others", id, name, strings.Join(variants, ", "), v)
						}
						break
					}
				}
				rep.add(b.name, "mod %d (%s): xml.env says %s=local, but the branch has no %s; dropped", id, name, key, f)
			case strings.HasPrefix(val, "./"):
				lines = append(lines, fmt.Sprintf("  %s: {source: mod, path: %s}", f, yamlQuote(val)))
			}
		}
		var names []string
		for n := range entries {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if used[n] {
				continue
			}
			files["files/"+n] = b.show(dir + n)
			rep.add(b.name, "mod %d (%s): %s is not used by xml.env (the legacy start ignored it); kept in the integration's files/", id, name, n)
		}
		hasXML = strings.Contains(strings.Join(lines, "\n"), ".xml:")
		var hooks string
		if _, ok := entries["start.sh"]; ok {
			files["hooks/start.sh"] = adaptStartScript(b.show(dir + "start.sh"))
			hooks = "hooks:\n  post_merge: [hooks/start.sh]\n"
			rep.add(b.name, "mod %d (%s): start.sh became a post_merge hook; it ran in the directory of the merged files, so `cd \"$DZO_STAGING\"` was added. Review it (it may use tools such as xmlstarlet that dzo does not depend on)", id, name)
		}
		if len(lines) == 0 && hooks == "" {
			continue
		}
		var y strings.Builder
		fmt.Fprintf(&y, "mod: %d\nname: %s\nfiles:\n%s\n", id, name, strings.Join(lines, "\n"))
		if len(lines) == 0 {
			y.Reset()
			fmt.Fprintf(&y, "mod: %d\nname: %s\n", id, name)
		}
		if hasXML {
			y.WriteString("normalize: [eventposdef-root, wrap-root, xml-decl]\n")
		}
		y.WriteString(hooks)
		if m := b.mods[id]; m != nil && len(m.aliases) > 0 {
			var as []string
			for _, a := range m.aliases {
				as = append(as, strconv.FormatUint(a, 10))
			}
			fmt.Fprintf(&y, "aliases: [%s]\n", strings.Join(as, ", "))
		}
		out[id] = integration{yaml: y.String(), files: files, sig: signature(files, y.String())}
	}
	return out
}

// adaptStartScript makes the legacy start.sh, which ran inside the directory of the merged files, run
// in the staging directory of the render.
func adaptStartScript(s []byte) []byte {
	text := string(s)
	line := "cd \"$DZO_STAGING\" || exit 1\n"
	if strings.HasPrefix(text, "#!") {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			return []byte(text[:i+1] + line + text[i+1:])
		}
	}
	return []byte("#!/usr/bin/env bash\n" + line + text)
}

// readOverlays collects files/custom/<name>/.
func readOverlays(b *branch, rep *report) map[string]overlayDir {
	out := map[string]overlayDir{}
	for _, e := range b.list {
		rest, ok := strings.CutPrefix(e.Path, "files/custom/")
		if !ok || e.Mode == "120000" {
			continue
		}
		name, rel, ok := strings.Cut(rest, "/")
		if !ok {
			continue // files/custom/README.md
		}
		ov := out[name]
		if ov.files == nil {
			ov.files = map[string][]byte{}
		}
		ov.files[rel] = b.show(e.Path)
		out[name] = ov
	}
	for n, ov := range out {
		ov.sig = signature(ov.files)
		out[n] = ov
	}
	dis := map[string]bool{}
	for _, e := range b.list {
		for _, p := range []string{"files/custom-disabled/", "files/disabled/", "files/editor_files_disabled/"} {
			if rest, ok := strings.CutPrefix(e.Path, p); ok {
				d, _, _ := strings.Cut(rest, "/")
				dis[strings.TrimSuffix(p, "/")+"/"+d] = true
			}
		}
	}
	var ds []string
	for d := range dis {
		ds = append(ds, d)
	}
	sort.Strings(ds)
	if len(ds) > 0 {
		rep.add(b.name, "not converted (disabled in the legacy repository): %s", strings.Join(ds, ", "))
	}
	return out
}
