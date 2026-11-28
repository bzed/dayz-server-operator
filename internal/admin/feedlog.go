// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// maxFeedLine is the longest line Load reads. dzo clips the events it writes far below this; a longer
// line (from an older version, or edited) is skipped, and the lines after it are still read.
const maxFeedLine = 1 << 20

// Load returns the newest FeedKeep events, oldest first. A missing file holds none, and lines that do
// not parse (a write cut short by a crash) or that are too long are skipped.
func (l *FeedLog) Load() ([]FeedEvent, error) {
	evs, _, err := l.scan()
	return evs, err
}

// scan reads the whole file line by line. It returns the newest FeedKeep events and the number of
// non-empty lines, whether they hold an event or not: that number decides when to compact.
func (l *FeedLog) scan() ([]FeedEvent, int, error) {
	f, err := os.Open(l.Path) //nolint:gosec // the operator's own db dir
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read feed log: %w", err)
	}
	defer func() { _ = f.Close() }() // read only
	var out []FeedEvent
	lines := 0
	rd := bufio.NewReaderSize(f, 64<<10)
	var line []byte
	over := false // the current line is longer than maxFeedLine
	for {
		part, isPrefix, err := rd.ReadLine()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, 0, fmt.Errorf("read feed log: %w", err)
			}
			break
		}
		if !over {
			if len(line)+len(part) > maxFeedLine {
				over = true
				line = line[:0]
			} else {
				line = append(line, part...)
			}
		}
		if isPrefix {
			continue
		}
		if over || len(line) > 0 {
			lines++
		}
		if !over {
			var ev FeedEvent
			if json.Unmarshal(line, &ev) == nil && ev.ID > 0 {
				out = append(out, ev)
				if len(out) >= 2*FeedKeep {
					out = append([]FeedEvent(nil), out[len(out)-FeedKeep:]...)
				}
			}
		}
		line, over = line[:0], false
	}
	if len(out) > FeedKeep {
		out = out[len(out)-FeedKeep:]
	}
	return out, lines, nil
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
		_, n, _ := l.scan() // counts what an earlier run left; the new line is in it
		l.lines = n
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
	evs, lines, err := l.scan()
	if err != nil {
		return err
	}
	h.feed = evs
	for _, ev := range evs {
		h.feedID = max(h.feedID, ev.ID)
	}
	l.lines = lines
	h.feedLog = l
	return nil
}
