// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// BansTxt is BattlEye's ban list in the instance's BattlEye directory; sidecar holds the SHA-256 of
// what dzo last wrote there.
const (
	BansTxt = "bans.txt"
	sidecar = ".dzo-bans.sha256"
)

// beReasonMax is the longest reason BattlEye keeps, in bytes (testdata/v1/NOTES.md Q4).
const beReasonMax = 64

// Entry is one line of bans.txt. Expires is a Unix time, or -1 for a permanent ban.
type Entry struct {
	Value   string // a GUID or an IP address
	Expires int64
	Reason  string
}

// BEReason makes text safe for one bans.txt line and one RCon kick command: no control characters or
// line breaks, single spaces, at most beReasonMax bytes of valid UTF-8.
func BEReason(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	for len(s) > beReasonMax {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "banned"
	}
	return s
}

// Entries returns the bans.txt lines of the bans in force on an instance (bans must be active ones):
// one per GUID and IP target, the longest ban winning when several cover the same value.
func Entries(bans []Ban, instance string) []Entry {
	by := map[string]Entry{}
	for _, b := range bans {
		if !b.AppliesTo(instance) {
			continue
		}
		exp := int64(-1)
		if b.ExpiresAt != nil {
			exp = b.ExpiresAt.Unix()
		}
		for _, t := range b.Targets {
			if t.Kind != TargetGUID && t.Kind != TargetIP {
				continue
			}
			old, ok := by[t.Value]
			if ok && (old.Expires == -1 || (exp != -1 && exp <= old.Expires)) {
				continue
			}
			by[t.Value] = Entry{Value: t.Value, Expires: exp, Reason: BEReason(b.Message)}
		}
	}
	out := make([]Entry, 0, len(by))
	for _, e := range by {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// FormatBansTxt renders entries the way BattlEye writes the file (testdata/v1).
func FormatBansTxt(es []Entry) []byte {
	var b bytes.Buffer
	for _, e := range es {
		fmt.Fprintf(&b, "%s %d %s\n", e.Value, e.Expires, BEReason(e.Reason))
	}
	return b.Bytes()
}

// ParseBansTxt reads bans.txt leniently: blank lines, comments and lines it does not understand are
// skipped.
func ParseBansTxt(b []byte) []Entry {
	var out []Entry
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		f := strings.SplitN(line, " ", 3)
		if len(f) < 2 {
			continue
		}
		exp, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}
		e := Entry{Value: f[0], Expires: exp}
		if len(f) == 3 {
			e.Reason = f[2]
		}
		out = append(out, e)
	}
	return out
}

func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// WriteBansFile writes dir/bans.txt atomically (mode 0600) when its content differs, and records its
// hash. changed reports whether it wrote.
func WriteBansFile(dir string, content []byte) (changed bool, err error) {
	path := filepath.Join(dir, BansTxt)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) { //nolint:gosec // the instance's BattlEye directory
		return false, os.WriteFile(filepath.Join(dir, sidecar), []byte(hashOf(content)+"\n"), 0o600)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, fmt.Errorf("moderation: %w", err)
	}
	if err := writeAtomic(path, content); err != nil {
		return false, err
	}
	if err := writeAtomic(filepath.Join(dir, sidecar), []byte(hashOf(content)+"\n")); err != nil {
		return false, err
	}
	return true, nil
}

// ChangedOutside reports whether dir/bans.txt exists and is not what dzo last wrote, and returns its
// content.
func ChangedOutside(dir string) (bool, []byte, error) {
	content, err := os.ReadFile(filepath.Join(dir, BansTxt)) //nolint:gosec // the instance's BattlEye directory
	if errors.Is(err, os.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("moderation: %w", err)
	}
	want, err := os.ReadFile(filepath.Join(dir, sidecar)) //nolint:gosec // dzo's own sidecar
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, nil, fmt.Errorf("moderation: %w", err)
	}
	if len(content) == 0 && err != nil {
		return false, content, nil // an empty file nobody wrote holds nothing to import
	}
	return strings.TrimSpace(string(want)) != hashOf(content), content, nil
}

// ImportEntries stores the entries of a bans.txt that dzo did not write as instance bans, skipping
// expired ones, values that are banned already and lines that are neither a GUID nor an IP.
func (s *Store) ImportEntries(ctx context.Context, instance string, es []Entry, now time.Time) (imported int, err error) {
	for _, e := range es {
		if e.Expires != -1 && e.Expires <= now.Unix() {
			continue
		}
		in := BanInput{Scope: ScopeInstances, Instances: []string{instance}, Message: BEReason(e.Reason)}
		in.Reason = "imported from bans.txt"
		if e.Reason != "" {
			in.Reason += ": " + e.Reason
		}
		if in.Message == "banned" {
			in.Message = ""
		}
		switch {
		case ValidGUID(e.Value):
			in.GUIDs = []string{e.Value}
		default:
			ip, err := ParseIP(e.Value)
			if err != nil {
				continue
			}
			in.IPs = []string{ip}
		}
		if e.Expires != -1 {
			t := time.Unix(e.Expires, 0).UTC()
			in.ExpiresAt = &t
		}
		if b, err := s.FindActive(ctx, instance, e.Value, e.Value, now); err != nil {
			return imported, err
		} else if b != nil {
			continue
		}
		if _, err := s.CreateBan(ctx, "import:"+BansTxt, in, now); err != nil {
			return imported, fmt.Errorf("moderation: import %s: %w", e.Value, err)
		}
		imported++
	}
	return imported, nil
}

// RenderAtStart writes an instance's bans.txt from a read-only database before the server starts, so
// bans hold even when dzo serve is down. It leaves the file alone when there is no database yet or when
// the file was changed outside dzo (serve imports it). The message says what it did.
func RenderAtStart(ctx context.Context, dbPath, instance, beDir string, now time.Time) (string, error) {
	st, err := OpenReadOnly(ctx, dbPath)
	if errors.Is(err, os.ErrNotExist) {
		return "no ban database yet; bans.txt left as it is", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = st.Close() }()
	if changed, _, err := ChangedOutside(beDir); err != nil {
		return "", err
	} else if changed {
		return "bans.txt was changed outside dzo; left as it is until dzo serve imports it", nil
	}
	bans, err := st.ActiveBans(ctx, instance, now)
	if err != nil {
		return "", err
	}
	es := Entries(bans, instance)
	if _, err := WriteBansFile(beDir, FormatBansTxt(es)); err != nil {
		return "", err
	}
	return fmt.Sprintf("bans.txt written (%d entries)", len(es)), nil
}
