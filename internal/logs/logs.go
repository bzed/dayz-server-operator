// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package logs rotates the log files in an instance's profiles directory into an archive outside
// the instance, and writes the crash summary after an unclean exit (FR-20).
//
// The profile directory collects logs from many writers. Rotation uses a list of regular
// expressions: per rule, the newest files stay in place (the running server may still write
// them) and every older match is archived.
package logs

import (
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/site"
)

// DefaultRules are the server's own files; BattlEye and mod logs are site configuration.
func DefaultRules() []site.LogRule {
	return []site.LogRule{
		{Match: `^DayZServer_x64_.*\.RPT$`},
		{Match: `^script_.*\.log$`},
		{Match: `^crash_.*\.log$`},
		{Match: `^DayZServer_x64_.*\.ADM$`},
		{Match: `\.mdmp$`, MaxAge: site.Age(14 * 24 * time.Hour)},
	}
}

const (
	defaultMinAge     = 10 * time.Minute
	defaultArchiveAge = 90 * 24 * time.Hour
)

// Action is one file to be archived.
type Action struct {
	Rel  string // slash path relative to profiles/
	Rule string // the match expression
	Size int64
	Mod  time.Time
}

// Plan is what a rotation would do.
type Plan struct {
	Move      []Action
	Excluded  []string // matched by a rule, but never touched
	Unmatched []Action // large files no rule matches (new mod logs show up here)
}

// excluded files are needed by dzo or the game and are never archived, whatever a rule says.
var excluded = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|/)serverDZ\.cfg$`),
	regexp.MustCompile(`(?i)^battleye/.*\.cfg$`),
	regexp.MustCompile(`(?i)^dzo-admin/`),
	regexp.MustCompile(`(?i)\.(json|xml)$`),
}

const unmatchedMin = 1 << 20

type compiled struct {
	rule site.LogRule
	re   *regexp.Regexp
	keep int
}

func compile(cfg site.LogsConfig) ([]compiled, error) {
	rules := cfg.Rotate
	if len(rules) == 0 {
		rules = DefaultRules()
	}
	keepDefault := 1
	if cfg.KeepNewest != nil {
		keepDefault = *cfg.KeepNewest
	}
	var out []compiled
	for _, r := range rules {
		re, err := regexp.Compile(r.Match)
		if err != nil {
			return nil, fmt.Errorf("logs.rotate: %q: %w", r.Match, err)
		}
		k := keepDefault
		if r.KeepNewest != nil {
			k = *r.KeepNewest
		}
		out = append(out, compiled{rule: r, re: re, keep: max(k, 0)})
	}
	return out, nil
}

// Validate checks the rule expressions.
func Validate(cfg site.LogsConfig) error {
	_, err := compile(cfg)
	return err
}

// MakePlan lists what a rotation of profiles would archive at time now. Only regular files below
// profiles are looked at; symbolic links are not followed.
func MakePlan(profiles string, cfg site.LogsConfig, now time.Time) (Plan, error) {
	rules, err := compile(cfg)
	if err != nil {
		return Plan{}, err
	}
	minAge := cfg.MinAge.Std()
	if minAge == 0 {
		minAge = defaultMinAge
	}
	series := make([][]Action, len(rules))
	var p Plan
	err = filepath.WalkDir(profiles, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(profiles, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		a := Action{Rel: rel, Size: fi.Size(), Mod: fi.ModTime()}
		for i, r := range rules {
			if r.re.MatchString(rel) {
				a.Rule = r.rule.Match
				if isExcluded(rel) {
					p.Excluded = append(p.Excluded, rel)
				} else {
					series[i] = append(series[i], a)
				}
				return nil
			}
		}
		if a.Size >= unmatchedMin && !isExcluded(rel) && !strings.HasPrefix(rel, "logs/") {
			p.Unmatched = append(p.Unmatched, a)
		}
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	for i, s := range series {
		sort.Slice(s, func(a, b int) bool {
			if !s[a].Mod.Equal(s[b].Mod) {
				return s[a].Mod.After(s[b].Mod)
			}
			return s[a].Rel > s[b].Rel
		})
		for j, a := range s {
			if j < rules[i].keep || now.Sub(a.Mod) < minAge {
				continue
			}
			p.Move = append(p.Move, a)
		}
	}
	sort.Slice(p.Move, func(a, b int) bool { return p.Move[a].Rel < p.Move[b].Rel })
	sort.Strings(p.Excluded)
	return p, nil
}

func isExcluded(rel string) bool {
	for _, re := range excluded {
		if re.MatchString(rel) {
			return true
		}
	}
	return false
}

// Rotate archives what MakePlan says into archive/<YYYY-MM-DD>/<relative path>[.gz] and removes
// the originals. It returns the files it moved.
func Rotate(profiles, archive string, cfg site.LogsConfig, now time.Time) ([]Action, error) {
	plan, err := MakePlan(profiles, cfg, now)
	if err != nil {
		return nil, err
	}
	gz := cfg.Archive.Compress != "none"
	var done []Action
	for _, a := range plan.Move {
		dest := filepath.Join(archive, now.Format("2006-01-02"), filepath.FromSlash(a.Rel))
		if gz {
			dest += ".gz"
		}
		dest = freeName(dest)
		if err := archiveFile(filepath.Join(profiles, filepath.FromSlash(a.Rel)), dest, gz); err != nil {
			return done, fmt.Errorf("archive %s: %w", a.Rel, err)
		}
		done = append(done, a)
	}
	return done, nil
}

func freeName(dest string) string {
	if _, err := os.Lstat(dest); err != nil {
		return dest
	}
	for i := 1; ; i++ {
		c := fmt.Sprintf("%s.%d", dest, i)
		if _, err := os.Lstat(c); err != nil {
			return c
		}
	}
}

func archiveFile(src, dest string, gz bool) error {
	in, err := os.Open(src) //nolint:gosec // a regular file below the profiles directory found by MakePlan
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640) //nolint:gosec // inside the archive
	if err != nil {
		return err
	}
	var w io.Writer = out
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(out)
		w = zw
	}
	_, err = io.Copy(w, in)
	if err == nil && zw != nil {
		err = zw.Close()
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	return os.Remove(src)
}

// Prune deletes archived files past their retention (the rule's max_age or the archive's, 90 days
// by default) and then the oldest ones until the archive fits max_size. It returns how many
// files it removed.
func Prune(archive string, cfg site.LogsConfig, now time.Time) (int, error) {
	rules, err := compile(cfg)
	if err != nil {
		return 0, err
	}
	maxAge := cfg.Archive.MaxAge.Std()
	if maxAge == 0 {
		maxAge = defaultArchiveAge
	}
	type f struct {
		path string
		size int64
		mod  time.Time
	}
	var files []f
	err = filepath.WalkDir(archive, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				files = append(files, f{file, fi.Size(), fi.ModTime()})
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	removed := 0
	kept := files[:0]
	for _, x := range files {
		age := maxAge
		rel, _ := filepath.Rel(archive, x.path)
		parts := strings.SplitN(filepath.ToSlash(rel), "/", 2) // the first level is the date
		if len(parts) == 2 {
			name := strings.TrimSuffix(path.Clean(parts[1]), ".gz")
			for _, r := range rules {
				if r.re.MatchString(name) {
					if r.rule.MaxAge > 0 {
						age = r.rule.MaxAge.Std()
					}
					break
				}
			}
		}
		if now.Sub(x.mod) > age {
			if os.Remove(x.path) == nil {
				removed++
			}
			continue
		}
		kept = append(kept, x)
	}
	if limit := int64(cfg.Archive.MaxSize); limit > 0 {
		var total int64
		for _, x := range kept {
			total += x.size
		}
		sort.Slice(kept, func(a, b int) bool { return kept[a].mod.Before(kept[b].mod) })
		for _, x := range kept {
			if total <= limit {
				break
			}
			if os.Remove(x.path) == nil {
				removed++
				total -= x.size
			}
		}
	}
	removeEmptyDirs(archive)
	return removed, nil
}

func removeEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // fails while it still has files
	}
}

// ArchiveEntry is one archived file.
type ArchiveEntry struct {
	Path string // relative to the archive
	Size int64
	Mod  time.Time
}

// List returns the archived files that changed since since (zero: all) and match re (nil: all),
// oldest first.
func List(archive string, since time.Time, re *regexp.Regexp) ([]ArchiveEntry, error) {
	var out []ArchiveEntry
	err := filepath.WalkDir(archive, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil || fi.ModTime().Before(since) {
			return nil
		}
		rel, _ := filepath.Rel(archive, file)
		rel = filepath.ToSlash(rel)
		if re == nil || re.MatchString(rel) {
			out = append(out, ArchiveEntry{rel, fi.Size(), fi.ModTime()})
		}
		return nil
	})
	sort.Slice(out, func(a, b int) bool { return out[a].Mod.Before(out[b].Mod) })
	return out, err
}

// ArchiveSize is the total size of the archive in bytes.
func ArchiveSize(archive string) int64 {
	es, _ := List(archive, time.Time{}, nil)
	var n int64
	for _, e := range es {
		n += e.Size
	}
	return n
}

// crashFiles are the logs a crash summary quotes: the newest file of each pattern.
var crashFiles = []string{"error.log", "script_*.log", "crash_*.log", "DayZServer_x64_*.RPT"}

// CrashSummary returns the last lines of the newest server logs in profiles, for the journal and
// Discord after an unclean exit. It has to run before the next start rotates them.
func CrashSummary(profiles string, lines int) string {
	var b strings.Builder
	for _, pat := range crashFiles {
		matches, _ := filepath.Glob(filepath.Join(profiles, pat))
		var newest string
		var mod time.Time
		for _, m := range matches {
			if fi, err := os.Lstat(m); err == nil && fi.Mode().IsRegular() && fi.ModTime().After(mod) {
				newest, mod = m, fi.ModTime()
			}
		}
		if newest == "" {
			continue
		}
		fmt.Fprintf(&b, "--- %s (last %d lines)\n%s\n", filepath.Base(newest), lines, tail(newest, lines))
	}
	return b.String()
}

func tail(file string, n int) string {
	f, err := os.Open(file) //nolint:gosec // a log file found by CrashSummary
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	const window = 64 << 10
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	off := max(fi.Size()-window, 0)
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return ""
	}
	ls := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if off > 0 && len(ls) > 0 {
		ls = ls[1:] // the first line is cut
	}
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}
