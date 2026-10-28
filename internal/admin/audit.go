// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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
