// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// AuditEntry is one admin action (§C16: who, what, which player/vehicle,
// result). Source says where it came from: api, web or cli.
type AuditEntry struct {
	Time   time.Time `json:"time"`
	Actor  string    `json:"actor"`
	Source string    `json:"source"`
	// Client is the address of the browser behind a web action. It is personal data: dzo serve blanks it
	// after serve.audit_ip_retention_days.
	Client   string `json:"client,omitempty"`
	Instance string `json:"instance"`
	Action   string `json:"action"`
	Target   string `json:"target,omitempty"`
	Detail   string `json:"detail,omitempty"`
	OK       bool   `json:"ok"`
	Result   string `json:"result,omitempty"`
}

// Audit is an append-only JSON-lines file. It is deliberately not a
// database: one line per admin action is small, greppable and survives
// whatever the DB layer later becomes.
type Audit struct {
	Path string
	// OnLog, when set, is called with every entry after it was written. It must not block.
	OnLog func(AuditEntry)
	now   func() time.Time
	mu    sync.Mutex
}

// NewAudit returns an Audit writing to path (created 0600 on first use).
func NewAudit(path string) *Audit { return &Audit{Path: path, now: time.Now} }

// Log appends one entry.
func (a *Audit) Log(e AuditEntry) error {
	if e.Time.IsZero() {
		e.Time = a.now().UTC()
	}
	if err := a.write(e); err != nil {
		return err
	}
	if a.OnLog != nil {
		a.OnLog(e)
	}
	return nil
}

func (a *Audit) write(e AuditEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o750); err != nil {
		return err
	}
	unlock, err := a.lockFile()
	if err != nil {
		return err
	}
	defer unlock()
	f, err := os.OpenFile(a.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path is operator config
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Tail returns the newest limit entries (optionally of one instance), newest
// first.
func (a *Audit) Tail(instance string, limit int) ([]AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.Open(a.Path) //nolint:gosec // path is operator config
	if os.IsNotExist(err) {
		return []AuditEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var all []AuditEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var e AuditEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if instance == "" || e.Instance == instance {
			all = append(all, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := []AuditEntry{}
	for i := len(all) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, all[i])
	}
	return out, nil
}

// lockFile takes an exclusive lock on <audit path>.lock, so that another process that appends to the log
// (`dzo user`) and the daily expiry of client addresses never overlap: a rewrite of the file replaces it,
// and a line appended to the old file meanwhile would be lost. The caller holds a.mu.
func (a *Audit) lockFile() (unlock func(), err error) {
	f, err := os.OpenFile(a.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path is operator config
	if err != nil {
		return nil, fmt.Errorf("audit lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // G115: a file descriptor fits an int
		_ = f.Close()
		return nil, fmt.Errorf("audit lock: %w", err)
	}
	return func() { _ = f.Close() }, nil // closing releases the lock
}

// ForgetClients blanks the client address of the entries from before the given time and returns how many
// entries changed. The entries themselves stay. The file is only rewritten (through a temporary file in the
// same directory, mode 0600) when something changes.
func (a *Audit) ForgetClients(before time.Time) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := os.Stat(a.Path); os.IsNotExist(err) {
		return 0, nil
	}
	unlock, err := a.lockFile()
	if err != nil {
		return 0, err
	}
	defer unlock()
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return 0, err
	}
	var out bytes.Buffer
	changed := 0
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var e AuditEntry
		if json.Unmarshal(line, &e) != nil || e.Client == "" || !e.Time.Before(before) {
			out.Write(line)
			continue
		}
		e.Client = ""
		b, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		out.Write(append(b, '\n'))
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(a.Path), filepath.Base(a.Path)+".tmp*")
	if err != nil {
		return 0, err
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(out.Bytes())
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(name, a.Path)
		}
		if err != nil {
			_ = os.Remove(name)
			return 0, err
		}
		return changed, nil
	}
	_ = tmp.Close()
	_ = os.Remove(name)
	return 0, fmt.Errorf("audit: %s: cannot set the mode", name)
}
