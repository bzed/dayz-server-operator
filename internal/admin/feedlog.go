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
)

// FeedLog is the on-disk copy of a hub's feed: append-only JSON lines, compacted to the newest FeedKeep
// lines once the file holds 2*FeedKeep (rewritten to a temp file and renamed). It holds chat and
// positions, so the file is 0600.
type FeedLog struct {
	Path string

	lines int // lines in the file, -1 until counted
}

// Load returns the newest FeedKeep events, oldest first. A missing file holds none, and lines that do
// not parse (a write cut short by a crash) are skipped.
func (l *FeedLog) Load() ([]FeedEvent, error) {
	b, err := os.ReadFile(l.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read feed log: %w", err)
	}
	var out []FeedEvent
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var ev FeedEvent
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.ID > 0 {
			out = append(out, ev)
		}
	}
	if len(out) > FeedKeep {
		out = out[len(out)-FeedKeep:]
	}
	return out, nil
}

// Append writes one event, and compacts the file when it has grown to 2*FeedKeep lines.
func (l *FeedLog) Append(ev FeedEvent) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encode feed event: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o750); err != nil {
		return fmt.Errorf("create feed log dir: %w", err)
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the operator's own db dir
	if err != nil {
		return fmt.Errorf("open feed log: %w", err)
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write feed log: %w", werr)
	}
	if l.lines == 0 {
		old, _ := l.Load() // counts what an earlier run left; the new line is in it
		l.lines = len(old)
	} else {
		l.lines++
	}
	if l.lines >= 2*FeedKeep {
		return l.compact()
	}
	return nil
}

func (l *FeedLog) compact() error {
	keep, err := l.Load()
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, ev := range keep {
		line, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("encode feed event: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	tmp := l.Path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write compacted feed log: %w", err)
	}
	if err := os.Rename(tmp, l.Path); err != nil {
		return fmt.Errorf("replace feed log: %w", err)
	}
	l.lines = len(keep)
	return nil
}

// UseFeedLog loads the log into the ring (IDs continue after the highest loaded one) and from then on
// appends every event to it. A hub that already uses a log at that path is left alone.
func (h *Hub) UseFeedLog(l *FeedLog) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.feedLog != nil && h.feedLog.Path == l.Path {
		return nil
	}
	evs, err := l.Load()
	if err != nil {
		return err
	}
	h.feed = evs
	for _, ev := range evs {
		h.feedID = max(h.feedID, ev.ID)
	}
	l.lines = len(evs)
	h.feedLog = l
	return nil
}
