// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBatchNotifier records every Notify/NotifyBatch call.
type fakeBatchNotifier struct {
	mu      sync.Mutex
	single  []Event
	batches [][]Event
	err     error
}

func (f *fakeBatchNotifier) Notify(_ context.Context, ev Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.single = append(f.single, ev)
	return f.err
}

func (f *fakeBatchNotifier) NotifyBatch(_ context.Context, _ Kind, events []Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, events)
	return f.err
}

func (f *fakeBatchNotifier) snapshot() (single []Event, batches [][]Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Event(nil), f.single...), append([][]Event(nil), f.batches...)
}

func TestCoalescerDisabledDeliversImmediately(t *testing.T) {
	fake := &fakeBatchNotifier{}
	c := NewCoalescer(fake, 0, nil)
	err := c.Notify(t.Context(), Event{Kind: KindHealthUnhealthy, Instance: "a"})
	require.NoError(t, err, "Notify")
	single, batches := fake.snapshot()
	require.Len(t, single, 1)
	require.Empty(t, batches)
}

func TestCoalescerBuffersUntilFlush(t *testing.T) {
	fake := &fakeBatchNotifier{}
	c := NewCoalescer(fake, time.Hour, nil) // long window: only Flush() delivers in this test
	for _, inst := range []string{"a", "b", "c"} {
		err := c.Notify(t.Context(), Event{Kind: KindModUpdateDetected, Instance: inst})
		require.NoError(t, err, "Notify")
	}
	_, batches := fake.snapshot()
	require.Empty(t, batches)

	err := c.Flush()
	require.NoError(t, err, "Flush")
	_, batches = fake.snapshot()
	require.Len(t, batches, 1)
	require.Len(t, batches[0], 3)
}

func TestCoalescerSeparatesKinds(t *testing.T) {
	fake := &fakeBatchNotifier{}
	c := NewCoalescer(fake, time.Hour, nil)
	err := c.Notify(t.Context(), Event{Kind: KindModUpdateDetected})
	require.NoError(t, err, "Notify")
	err = c.Notify(t.Context(), Event{Kind: KindHealthUnhealthy})
	require.NoError(t, err, "Notify")
	err = c.Flush()
	require.NoError(t, err, "Flush")
	_, batches := fake.snapshot()
	require.Len(t, batches, 2)
}

func TestCoalescerFlushIsIdempotent(t *testing.T) {
	fake := &fakeBatchNotifier{}
	c := NewCoalescer(fake, time.Hour, nil)
	err := c.Notify(t.Context(), Event{Kind: KindHealthUnhealthy})
	require.NoError(t, err, "Notify")
	err = c.Flush()
	require.NoError(t, err, "Flush")
	err = c.Flush()
	require.NoError(t, err, "second Flush")
	_, batches := fake.snapshot()
	require.Len(t, batches, 1)
}

func TestCoalescerFlushWithNothingPending(t *testing.T) {
	fake := &fakeBatchNotifier{}
	c := NewCoalescer(fake, time.Hour, nil)
	err := c.Flush()
	require.NoError(t, err, "Flush")
	_, batches := fake.snapshot()
	require.Empty(t, batches)
}

func TestCoalescerFlushReturnsFirstError(t *testing.T) {
	fake := &fakeBatchNotifier{err: errors.New("boom")}
	c := NewCoalescer(fake, time.Hour, nil)
	err := c.Notify(t.Context(), Event{Kind: KindHealthUnhealthy})
	require.NoError(t, err, "Notify")
	err = c.Flush()
	require.Error(t, err, "expected Flush to report the delivery error")
}

func TestCoalescerAutoFlushOnTimer(t *testing.T) {
	fake := &fakeBatchNotifier{}
	var onErrCalled atomic.Bool
	c := NewCoalescer(fake, 20*time.Millisecond, func(error) { onErrCalled.Store(true) })

	err := c.Notify(t.Context(), Event{Kind: KindHealthUnhealthy, Instance: "a"})
	require.NoError(t, err, "Notify")
	err = c.Notify(t.Context(), Event{Kind: KindHealthUnhealthy, Instance: "b"})
	require.NoError(t, err, "Notify")

	require.Eventually(t, func() bool {
		_, batches := fake.snapshot()
		return len(batches) == 1
	}, 2*time.Second, time.Millisecond, "timed out waiting for the auto-flush to deliver the batch")
	_, batches := fake.snapshot()
	require.Len(t, batches[0], 2)
	assert.False(t, onErrCalled.Load(), "onError should not have been called on success")
}

func TestCoalescerOnErrorCalledFromAutoFlush(t *testing.T) {
	fake := &fakeBatchNotifier{err: errors.New("boom")}
	errCh := make(chan error, 1)
	c := NewCoalescer(fake, 10*time.Millisecond, func(err error) { errCh <- err })

	err := c.Notify(t.Context(), Event{Kind: KindHealthUnhealthy})
	require.NoError(t, err, "Notify")

	select {
	case err := <-errCh:
		require.Error(t, err, "expected a non-nil error")
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timed out waiting for onError")
	}
}
