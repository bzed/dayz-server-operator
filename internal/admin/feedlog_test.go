// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFeedLogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed", "alpha.jsonl")
	l := &FeedLog{Path: path}
	for i := int64(1); i <= 3; i++ {
		require.NoError(t, l.Append(FeedEvent{ID: i, Kind: FeedChat, Text: "x"}))
	}
	got, err := (&FeedLog{Path: path}).Load()
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, ids(got))
}

func TestFeedLogMissingFile(t *testing.T) {
	got, err := (&FeedLog{Path: filepath.Join(t.TempDir(), "none.jsonl")}).Load()
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestFeedLogSkipsBadLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"id":1,"kind":"chat"}`+"\n"+`{"id":2,"ki`), 0o600))
	got, err := (&FeedLog{Path: path}).Load()
	require.NoError(t, err)
	require.Equal(t, []int64{1}, ids(got))
}

func TestFeedLogCompacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	l := &FeedLog{Path: path}
	for i := int64(1); i <= 2*FeedKeep; i++ {
		require.NoError(t, l.Append(FeedEvent{ID: i, Kind: FeedChat}))
	}
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, FeedKeep, strings.Count(string(b), "\n"))
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	got, err := l.Load()
	require.NoError(t, err)
	require.EqualValues(t, FeedKeep+1, got[0].ID)
	require.EqualValues(t, 2*FeedKeep, got[len(got)-1].ID)
}

func TestUseFeedLogContinuesIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	l := &FeedLog{Path: path}
	for i := int64(1); i <= 5; i++ {
		require.NoError(t, l.Append(FeedEvent{ID: i, Kind: FeedChat}))
	}
	h, _ := testHub()
	require.NoError(t, h.UseFeedLog(&FeedLog{Path: path}))
	ev := h.AddFeed(FeedEvent{Kind: FeedChat})
	require.EqualValues(t, 6, ev.ID)
	require.Equal(t, []int64{1, 2, 3, 4, 5, 6}, ids(h.Feed(0, 0)))
	got, err := l.Load()
	require.NoError(t, err)
	require.Len(t, got, 6)
}

func TestAppendFailureIsLogged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.jsonl")
	h, _ := testHub()
	var logged []string
	h.Logf = func(f string, a ...any) { logged = append(logged, f) }
	require.NoError(t, h.UseFeedLog(&FeedLog{Path: path}))
	require.NoError(t, os.Mkdir(path, 0o750)) // appending to a directory fails
	h.AddFeed(FeedEvent{Kind: FeedChat})
	require.Len(t, h.Feed(0, 0), 1)
	require.Len(t, logged, 1)
}

func TestRegistryAddKeepsFeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	r := NewRegistry(t.TempDir())
	h := r.Add(NewHub("alpha"))
	require.NoError(t, h.UseFeedLog(&FeedLog{Path: path}))
	h.AddFeed(FeedEvent{Kind: FeedChat})
	again := r.Add(NewHub("alpha"))
	require.Same(t, h, again)
	again.AddFeed(FeedEvent{Kind: FeedChat})
	require.Equal(t, []int64{1, 2}, ids(again.Feed(0, 0)))
	got, err := (&FeedLog{Path: path}).Load()
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestFeedLogSkipsLongLineAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	huge := `{"id":2,"kind":"chat","text":"` + strings.Repeat("x", 3*maxFeedLine) + `"}`
	data := `{"id":1,"kind":"chat"}` + "\n" + huge + "\n" + `{"id":3,"kind":"chat"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	got, err := (&FeedLog{Path: path}).Load()
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3}, ids(got))

	// A long first line does not hide the events after it, and the line count includes it.
	require.NoError(t, os.WriteFile(path, []byte(huge+"\n"+`{"id":3,"kind":"chat"}`+"\n"), 0o600))
	l := &FeedLog{Path: path}
	evs, lines, err := l.scan()
	require.NoError(t, err)
	require.Equal(t, []int64{3}, ids(evs))
	require.Equal(t, 2, lines)

	// A hub starting on that file keeps the events after the long line.
	h, _ := testHub()
	require.NoError(t, h.UseFeedLog(&FeedLog{Path: path}))
	require.Equal(t, []int64{3}, ids(h.Feed(0, 0)))
}

func TestFeedLogCompactionKeepsEventsAfterAnUnreadableLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.jsonl")
	huge := `{"id":1,"kind":"chat","text":"` + strings.Repeat("x", 2*maxFeedLine) + `"}`
	require.NoError(t, os.WriteFile(path, []byte(huge+"\n"), 0o600))
	l := &FeedLog{Path: path}
	for i := int64(2); i <= 2*FeedKeep; i++ {
		require.NoError(t, l.Append(FeedEvent{ID: i, Kind: FeedChat}))
	}
	got, err := l.Load()
	require.NoError(t, err)
	require.Len(t, got, FeedKeep)
	require.EqualValues(t, 2*FeedKeep, got[len(got)-1].ID)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Less(t, len(b), 1<<20)
}
