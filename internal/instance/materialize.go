// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bzed/dayz-server-operator/internal/quadlet"
)

// WriteContainerUnit renders spec (internal/quadlet) and writes it
// atomically to "<dir>/<spec.Name>.container", the systemd user quadlet
// directory (e.g. ~/.config/containers/systemd/). It returns the path
// written.
func WriteContainerUnit(dir string, spec quadlet.ContainerSpec) (string, error) {
	content, err := quadlet.RenderContainer(spec)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, spec.Name+".container")
	if err := writeUnitAtomic(path, content); err != nil {
		return "", err
	}
	return path, nil
}

// TimerUnit describes one dzo-managed periodic job: a oneshot .service
// paired with a .timer that triggers it (dzo-restart-<name>,
// dzo-update-check, ...; §C5's timer table). Unlike a quadlet .container,
// these are plain systemd units dzo renders itself.
type TimerUnit struct {
	Name        string // unit basename, e.g. "dzo-restart-deerisle"
	Description string
	ExecStart   string   // the paired .service's ExecStart command line
	OnCalendar  []string // systemd calendar expressions; at least one required
	Persistent  bool     // OnCalendar catch-up after downtime
}

// Validate checks the invariants WriteTimerUnit relies on.
func (t TimerUnit) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("instance: TimerUnit.Name is required")
	}
	if t.ExecStart == "" {
		return fmt.Errorf("instance: TimerUnit.ExecStart is required")
	}
	if len(t.OnCalendar) == 0 {
		return fmt.Errorf("instance: TimerUnit.OnCalendar must have at least one entry")
	}
	return nil
}

// ServicePathEnv is the PATH of generated services: user units get a minimal one, and Debian
// installs steamcmd in /usr/games.
const ServicePathEnv = "PATH=/usr/local/bin:/usr/bin:/bin:/usr/local/games:/usr/games"

// RenderService renders t's paired oneshot .service unit.
func (t TimerUnit) RenderService() (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	if t.Description != "" {
		fmt.Fprintf(&b, "[Unit]\nDescription=%s\n\n", t.Description)
	}
	fmt.Fprintf(&b, "[Service]\nType=oneshot\nEnvironment=%s\nExecStart=%s\n", ServicePathEnv, t.ExecStart)
	return b.String(), nil
}

// RenderTimer renders t's .timer unit.
func (t TimerUnit) RenderTimer() (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	if t.Description != "" {
		fmt.Fprintf(&b, "[Unit]\nDescription=%s\n\n", t.Description)
	}
	b.WriteString("[Timer]\n")
	for _, oc := range t.OnCalendar {
		fmt.Fprintf(&b, "OnCalendar=%s\n", oc)
	}
	fmt.Fprintf(&b, "Persistent=%t\n\n", t.Persistent)
	b.WriteString("[Install]\nWantedBy=timers.target\n")
	return b.String(), nil
}

// WriteTimerUnit renders and atomically writes both of t's unit files to
// dir, returning their paths (timer, service).
func WriteTimerUnit(dir string, t TimerUnit) (string, string, error) {
	timerContent, err := t.RenderTimer()
	if err != nil {
		return "", "", err
	}
	serviceContent, err := t.RenderService()
	if err != nil {
		return "", "", err
	}
	timerPath := filepath.Join(dir, t.Name+".timer")
	servicePath := filepath.Join(dir, t.Name+".service")
	if err := writeUnitAtomic(timerPath, timerContent); err != nil {
		return "", "", err
	}
	if err := writeUnitAtomic(servicePath, serviceContent); err != nil {
		return "", "", err
	}
	return timerPath, servicePath, nil
}

// writeUnitAtomic writes content to path via temp file + rename, the same
// pattern used throughout dzo for any file a running process might read
// mid-write (systemd reloads unit files from disk on `daemon-reload`).
func writeUnitAtomic(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".unit-*.tmp")
	if err != nil {
		return fmt.Errorf("instance: create temp unit file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup; the rename below removes it on success

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("instance: write temp unit file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("instance: close temp unit file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil { //nolint:gosec // unit files are read by systemd, world-readable is expected
		return fmt.Errorf("instance: chmod temp unit file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("instance: activate unit file %s: %w", path, err)
	}
	return nil
}
