// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/apiclient"
)

const (
	defaultEventLines = 20
	eventsMinBackoff  = time.Second
	eventsMaxBackoff  = 30 * time.Second
)

func newEventsCmd() *cobra.Command {
	var t apiTarget
	var follow, asJSON, noColor bool
	var lines int
	var kinds string
	cmd := &cobra.Command{
		Use:   "events <instance>",
		Short: "Show what happens in the game: kills, joins, building, chat, admin actions (needs `dzo serve`)",
		Long: "Prints the newest events of the instance's event feed, journalctl style, and exits; -f keeps following it until " +
			"Ctrl-C. The feed is kept by `dzo serve` over restarts, so this works right after one. --kind takes kinds " +
			"(kill, chat, ...) or the groups of the web page: " + strings.Join(eventGroupNames(), ", ") + ".",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if lines < 0 || lines > admin.FeedKeep {
				return fmt.Errorf("--lines must be between 0 and %d", admin.FeedKeep)
			}
			want, err := parseEventKinds(kinds)
			if err != nil {
				return err
			}
			c, err := t.client()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			ctx := ctxOf(cmd)
			if follow {
				var stop context.CancelFunc
				ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
				defer stop()
			}
			r := &eventsRunner{c: c, instance: args[0], kinds: want, json: asJSON, color: !asJSON && useColor(out, noColor), follow: follow,
				out: out, errOut: cmd.ErrOrStderr(), minBackoff: eventsMinBackoff, maxBackoff: eventsMaxBackoff}
			return r.run(ctx, lines)
		},
	}
	t.flags(cmd)
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep following new events until interrupted")
	cmd.Flags().IntVarP(&lines, "lines", "n", defaultEventLines, "how many of the newest events to show first")
	cmd.Flags().StringVar(&kinds, "kind", "", "only these kinds or groups, comma separated ("+strings.Join(eventGroupNames(), ", ")+")")
	cmd.Flags().BoolVar(&asJSON, "json", false, "one JSON object per event")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "no colours (also off when not a terminal or NO_COLOR is set)")
	return cmd
}

func eventGroupNames() []string {
	var out []string
	for _, g := range admin.FeedGroups {
		out = append(out, g.ID)
	}
	return out
}

// parseEventKinds reads --kind: kinds and group names; empty means everything (nil).
func parseEventKinds(spec string) (map[string]bool, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	valid := map[string][]string{}
	var names []string
	for _, g := range admin.FeedGroups {
		valid[g.ID] = g.Kinds
		names = append(names, g.ID)
		for _, k := range g.Kinds {
			valid[k] = []string{k}
			names = append(names, k)
		}
	}
	want := map[string]bool{}
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		kinds, ok := valid[name]
		if !ok {
			return nil, fmt.Errorf("unknown kind or group %q (want one of %s)", name, strings.Join(names, ", "))
		}
		for _, k := range kinds {
			want[k] = true
		}
	}
	return want, nil
}

// colorFor is the ANSI colour code of a kind's group, or "".
func colorFor(kind string) string {
	switch admin.FeedGroupOf(kind) {
	case "kills":
		return "31"
	case "raids":
		return "35"
	case "chat":
		return "36"
	case "players":
		return "32"
	case "admin":
		return "33"
	}
	return ""
}

// useColor is true when w is a terminal, NO_COLOR is unset and --no-color was not given.
func useColor(w io.Writer, noColor bool) bool {
	f, ok := w.(*os.File)
	return ok && !noColor && os.Getenv("NO_COLOR") == "" && isTerminal(f)
}

// eventsRunner prints the feed of one instance.
type eventsRunner struct {
	c          *apiclient.Client
	instance   string
	kinds      map[string]bool // nil: all
	json       bool
	color      bool
	follow     bool
	out        io.Writer
	errOut     io.Writer
	minBackoff time.Duration
	maxBackoff time.Duration

	last int64 // the newest id seen, printed or filtered out
}

func (r *eventsRunner) fetch(ctx context.Context, after int64, limit int) ([]admin.FeedEvent, error) {
	evs, err := r.c.Feed(ctx, r.instance, after, limit)
	if err != nil {
		return nil, fmt.Errorf("events of %s: %w", r.instance, err)
	}
	return evs, nil
}

// run prints the newest n events and, when following, everything after them until ctx ends.
func (r *eventsRunner) run(ctx context.Context, n int) error {
	if n > 0 {
		evs, err := r.fetch(ctx, 0, n)
		if err != nil {
			return err
		}
		for _, ev := range evs {
			r.show(ev)
		}
	}
	if !r.follow {
		return nil
	}
	return r.followStream(ctx)
}

func (r *eventsRunner) followStream(ctx context.Context) error {
	backoff := r.minBackoff
	for {
		err := r.c.StreamOnly(ctx, r.instance, "feed", func(kind string, data json.RawMessage) {
			var ev admin.FeedEvent
			if kind != "feed" || json.Unmarshal(data, &ev) != nil {
				return
			}
			backoff = r.minBackoff
			r.frame(ctx, ev)
		})
		if ctx.Err() != nil {
			return nil
		}
		var ae *apiclient.Error
		if errors.As(err, &ae) && ae.Status < 500 {
			return fmt.Errorf("events of %s: %w", r.instance, err) // a wrong token or instance does not get better
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, r.maxBackoff)
		r.catchUp(ctx)
	}
}

// frame prints one event of the stream. One that skips ids was passed over by a slow reader: the
// events between are fetched first.
func (r *eventsRunner) frame(ctx context.Context, ev admin.FeedEvent) {
	if ev.ID <= r.last {
		return
	}
	if r.last > 0 && ev.ID > r.last+1 {
		r.catchUp(ctx)
	}
	if ev.ID > r.last {
		r.show(ev)
	}
}

// catchUp prints what came after the last event; ids that are gone from the ring are marked.
func (r *eventsRunner) catchUp(ctx context.Context) {
	evs, err := r.fetch(ctx, r.last, 0)
	if err != nil {
		if ctx.Err() == nil {
			_, _ = fmt.Fprintln(r.errOut, err)
		}
		return
	}
	if len(evs) > 0 && r.last > 0 && evs[0].ID > r.last+1 {
		if r.json {
			_, _ = fmt.Fprintln(r.errOut, "-- events missed --")
		} else {
			_, _ = fmt.Fprintln(r.out, "-- events missed --")
		}
	}
	for _, ev := range evs {
		r.show(ev)
	}
}

func (r *eventsRunner) show(ev admin.FeedEvent) {
	if ev.ID <= r.last {
		return
	}
	r.last = ev.ID
	if r.kinds != nil && !r.kinds[ev.Kind] {
		return
	}
	if r.json {
		b, err := json.Marshal(ev)
		if err == nil {
			_, _ = fmt.Fprintln(r.out, string(b))
		}
		return
	}
	line := fmt.Sprintf("%s %-11s %s", ev.Time.Local().Format("2006-01-02 15:04:05"), ev.KindText(), ev.Summary())
	if ev.HasPos {
		line += fmt.Sprintf("  @ %.0f / %.0f", ev.X, ev.Z)
	}
	if c := colorFor(ev.Kind); r.color && c != "" {
		line = "\x1b[" + c + "m" + line + "\x1b[0m"
	}
	_, _ = fmt.Fprintln(r.out, line)
}
