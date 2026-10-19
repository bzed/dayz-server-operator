// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package steam

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadStatusMissingFileReturnsZeroValue(t *testing.T) {
	s, err := LoadStatus(filepath.Join(t.TempDir(), "missing.json"))
	require.NoError(t, err, "LoadStatus")
	require.Empty(t, s.Account)
	require.False(t, s.AuthRequired)
}

func TestStatusSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	want := Status{Account: "bob", AuthRequired: true, LastFailureAt: &now, LastFailureReason: FailureInvalidPassword}

	err := want.Save(path)
	require.NoError(t, err, "Save")
	got, err := LoadStatus(path)
	require.NoError(t, err, "LoadStatus")
	require.Equal(t, want.Account, got.Account)
	require.Equal(t, want.AuthRequired, got.AuthRequired)
	require.Equal(t, want.LastFailureReason, got.LastFailureReason)
	require.NotNil(t, got.LastFailureAt)
	require.True(t, got.LastFailureAt.Equal(now))
}

func TestLoadStatusInvalidJSONErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	err := os.WriteFile(path, []byte("not json"), 0o600)
	require.NoError(t, err, "WriteFile")
	_, err = LoadStatus(path)
	require.Error(t, err, "expected an error for invalid JSON")
}

func TestStatusSaveMissingParentDirErrors(t *testing.T) {
	s := Status{Account: "bob"}
	err := s.Save(filepath.Join(t.TempDir(), "missing-dir", "status.json"))
	require.Error(t, err, "expected an error when the parent directory does not exist")
}

func TestStatusRecordSuccessClearsAuthRequired(t *testing.T) {
	s := Status{Account: "bob", AuthRequired: true, LastFailureReason: FailureInvalidPassword}
	now := time.Now()
	s.Record("bob", Result{Success: true}, now)

	assert.False(t, s.AuthRequired, "AuthRequired should be cleared on a successful login")
	assert.Empty(t, s.LastFailureReason)
	require.NotNil(t, s.LastSuccessAt)
	assert.True(t, s.LastSuccessAt.Equal(now))
}

func TestStatusRecordAuthFailureSetsAuthRequired(t *testing.T) {
	s := Status{}
	now := time.Now()
	s.Record("bob", Result{Success: false, Reason: FailureInvalidPassword}, now)

	assert.True(t, s.AuthRequired, "expected AuthRequired to be set on an auth failure")
	require.NotNil(t, s.LastFailureAt)
	assert.True(t, s.LastFailureAt.Equal(now))
	assert.Equal(t, FailureInvalidPassword, s.LastFailureReason)
}

func TestStatusRecordTransientFailureDoesNotSetAuthRequired(t *testing.T) {
	s := Status{}
	s.Record("bob", Result{Success: false, Reason: FailureRateLimited}, time.Now())
	assert.False(t, s.AuthRequired, "a rate-limit failure should not require re-authentication")
}

func TestIsAuthFailure(t *testing.T) {
	authFailures := []FailureReason{
		FailureInvalidPassword, FailureGuardCodeMismatch, FailureAccountLogonDenied, FailureCachedCredentialsGone,
	}
	for _, r := range authFailures {
		assert.True(t, IsAuthFailure(r), "IsAuthFailure(%v) = false, want true", r)
	}
	transient := []FailureReason{FailureRateLimited, FailureNoSubscription, FailureUnknown, ""}
	for _, r := range transient {
		assert.False(t, IsAuthFailure(r), "IsAuthFailure(%v) = true, want false", r)
	}
}
