// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGateStateMissingFileReturnsZeroValue(t *testing.T) {
	s, err := LoadGateState(filepath.Join(t.TempDir(), "missing.json"))
	require.NoError(t, err, "LoadGateState")
	assert.False(t, s.Failed, "s = %+v, want not failed", s)
}

func TestGateStateSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.json")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	want := GateState{Failed: true, InputHash: "sha256:abc", Reason: "render: bad XML", At: now}

	err := want.Save(path)
	require.NoError(t, err, "Save")
	got, err := LoadGateState(path)
	require.NoError(t, err, "LoadGateState")
	require.Equal(t, want.Failed, got.Failed)
	require.Equal(t, want.InputHash, got.InputHash)
	require.Equal(t, want.Reason, got.Reason)
	assert.True(t, got.At.Equal(want.At), "got = %+v, want %+v", got, want)
}

func TestLoadGateStateInvalidJSONErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.json")
	err := os.WriteFile(path, []byte("not json"), 0o600)
	require.NoError(t, err, "WriteFile")
	_, err = LoadGateState(path)
	require.Error(t, err)
}

func TestGateStateSaveMissingParentDirErrors(t *testing.T) {
	s := GateState{Failed: true}
	err := s.Save(filepath.Join(t.TempDir(), "missing-dir", "gate.json"))
	require.Error(t, err)
}

func TestFailureGateBlockedSameHash(t *testing.T) {
	g := FailureGate{}
	g.RecordFailure("sha256:abc", "bad config", time.Now())
	assert.True(t, g.Blocked("sha256:abc"), "expected Blocked to be true for the same input hash")
}

func TestFailureGateNotBlockedDifferentHash(t *testing.T) {
	g := FailureGate{}
	g.RecordFailure("sha256:abc", "bad config", time.Now())
	assert.False(t, g.Blocked("sha256:def"), "expected Blocked to be false once the input hash changes")
}

func TestFailureGateNotBlockedWhenNeverFailed(t *testing.T) {
	g := FailureGate{}
	assert.False(t, g.Blocked("sha256:anything"), "expected Blocked to be false with no prior failure")
}

func TestFailureGateRecordSuccessClears(t *testing.T) {
	g := FailureGate{}
	g.RecordFailure("sha256:abc", "bad config", time.Now())
	g.RecordSuccess()
	assert.False(t, g.Blocked("sha256:abc"), "expected Blocked to be false after RecordSuccess")
}

func TestFailureGateAckClears(t *testing.T) {
	g := FailureGate{}
	g.RecordFailure("sha256:abc", "bad config", time.Now())
	g.Ack()
	assert.False(t, g.Blocked("sha256:abc"), "expected Blocked to be false after Ack")
}

func TestHashInputStable(t *testing.T) {
	a := HashInput([]byte("same input"))
	b := HashInput([]byte("same input"))
	c := HashInput([]byte("different input"))
	assert.Equal(t, b, a)
	assert.NotEqual(t, c, a)
}
