// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFakeSteamcmd writes a shell script that scripts steamcmd for a job
// to drive. Each job runs the fake binary twice - once via ensureLoggedIn
// for a bare "+login <account> +quit", and again with the job's own
// arguments - so the script branches on "$*" to only print job-specific
// lines when job-specific args are actually present, the same way real
// steamcmd would never mention app_update/workshop_download_item during a
// plain login check. Real steamcmd output for these operations is not
// verified against a live account from this environment; the success line
// quoted here ("Success. Downloaded item <id>") is taken directly from the
// plan's record of the legacy tool's own detection logic, and the
// app_update line is widely documented steamcmd usage - see the package
// doc comment.
func writeFakeSteamcmd(t testing.TB, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-steamcmd.sh")
	err := os.WriteFile(path, []byte("#!/bin/sh\necho \"Waiting for user info...OK\"\n"+script), 0o755)
	require.NoError(t, err, "write fake steamcmd")
	return path
}

// writeFakeSteamcmdNoLogin is like writeFakeSteamcmd but for scripts that
// need to control the login line themselves (e.g. simulating a rejected
// cached session).
func writeFakeSteamcmdNoLogin(t testing.TB, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-steamcmd.sh")
	err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755)
	require.NoError(t, err, "write fake steamcmd")
	return path
}

func TestRunAppUpdateSuccess(t *testing.T) {
	script := writeFakeSteamcmd(t, `
case "$*" in
  *app_update*) echo "Success! App '223350' fully installed." ;;
esac
`)
	result, err := RunAppUpdate(t.Context(), AppUpdateOptions{
		Command: script, Account: "bob", AppID: 223350, InstallDir: t.TempDir(), Validate: true,
	})
	require.NoError(t, err, "RunAppUpdate")
	require.True(t, result.Success, "result = %+v", result)
	require.False(t, result.AuthRequired, "result = %+v", result)
}

func TestRunAppUpdateAuthRequired(t *testing.T) {
	// A stale cached session can be rejected outright, with no prompt at
	// all - this must be classified as AuthRequired just like a declined
	// prompt (see ensureLoggedIn's doc comment). No prompt also means no
	// blocked "read" left over in the fake steamcmd for Run's cleanup to
	// wait out, so this stays fast.
	script := writeFakeSteamcmdNoLogin(t, `
echo "FAILED (Invalid Password)"
`)
	result, err := RunAppUpdate(t.Context(), AppUpdateOptions{
		Command: script, Account: "bob", AppID: 223350, InstallDir: t.TempDir(),
	})
	require.NoError(t, err, "RunAppUpdate")
	require.True(t, result.AuthRequired, "result = %+v, want AuthRequired", result)
	require.False(t, result.Success, "result = %+v, want AuthRequired", result)
}

func TestRunAppUpdateMissingOptionsErrors(t *testing.T) {
	_, err := RunAppUpdate(t.Context(), AppUpdateOptions{})
	require.Error(t, err)
}

func TestRunAppUpdateFailureWithoutSuccessMarker(t *testing.T) {
	script := writeFakeSteamcmd(t, `
case "$*" in
  *app_update*)
    echo "ERROR! Failed to install app '223350' (No space left on device)"
    exit 7
    ;;
esac
`)
	result, err := RunAppUpdate(t.Context(), AppUpdateOptions{
		Command: script, Account: "bob", AppID: 223350, InstallDir: t.TempDir(),
	})
	require.NoError(t, err, "RunAppUpdate")
	require.False(t, result.Success, "result = %+v, want a plain (non-auth) failure", result)
	require.False(t, result.AuthRequired, "result = %+v, want a plain (non-auth) failure", result)
}

func TestRunAppUpdateBetaBranch(t *testing.T) {
	var gotArgs string
	script := writeFakeSteamcmd(t, `
case "$*" in
  *app_update*)
    echo "$@" > "$(dirname "$0")/args.txt"
    echo "Success! App '1042420' fully installed."
    ;;
esac
`)
	dir := filepath.Dir(script)
	result, err := RunAppUpdate(t.Context(), AppUpdateOptions{
		Command: script, Account: "bob", AppID: 1042420, InstallDir: t.TempDir(),
		BetaBranch: "experimental", BetaPassword: "s3cr3t",
	})
	require.NoError(t, err, "RunAppUpdate")
	require.True(t, result.Success, "result = %+v", result)
	data, err := os.ReadFile(filepath.Join(dir, "args.txt")) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read args.txt")
	gotArgs = string(data)
	for _, want := range []string{"-beta", "experimental", "-betapassword", "s3cr3t"} {
		assert.True(t, contains(gotArgs, want), "args = %q, want it to contain %q", gotArgs, want)
	}
}

func contains(s, substr string) bool { return containsFold(s, substr) }

func TestRunWorkshopDownloadAllSucceed(t *testing.T) {
	script := writeFakeSteamcmd(t, `
case "$*" in
  *workshop_download_item*)
    echo "Success. Downloaded item 111111 to..."
    echo "Success. Downloaded item 222222 to..."
    ;;
esac
`)
	result, err := RunWorkshopDownload(t.Context(), WorkshopDownloadOptions{
		Command: script, Account: "bob", WorkshopAppID: 221100, ItemIDs: []uint64{111111, 222222},
	})
	require.NoError(t, err, "RunWorkshopDownload")
	require.True(t, result.Success, "result = %+v", result)
	require.Empty(t, result.FailedItems)
}

func TestRunWorkshopDownloadPartialFailure(t *testing.T) {
	script := writeFakeSteamcmd(t, `
case "$*" in
  *workshop_download_item*)
    echo "Success. Downloaded item 111111 to..."
    echo "ERROR! Download item 222222 failed (Failure)."
    ;;
esac
`)
	result, err := RunWorkshopDownload(t.Context(), WorkshopDownloadOptions{
		Command: script, Account: "bob", WorkshopAppID: 221100, ItemIDs: []uint64{111111, 222222},
	})
	require.NoError(t, err, "RunWorkshopDownload")
	require.False(t, result.Success, "result = %+v, want Success=false", result)
	msg, ok := result.FailedItems[222222]
	require.True(t, ok, "FailedItems[222222] = %q, ok=%v", msg, ok)
	assert.NotEmpty(t, msg, "FailedItems[222222] = %q, ok=%v", msg, ok)
	_, ok = result.FailedItems[111111]
	assert.False(t, ok, "111111 should not be in FailedItems: %+v", result.FailedItems)
}

func TestRunWorkshopDownloadMissingResultLineCountsAsFailed(t *testing.T) {
	script := writeFakeSteamcmd(t, `
case "$*" in
  *workshop_download_item*) echo "Success. Downloaded item 111111 to..." ;;
esac
`)
	result, err := RunWorkshopDownload(t.Context(), WorkshopDownloadOptions{
		Command: script, Account: "bob", WorkshopAppID: 221100, ItemIDs: []uint64{111111, 333333},
	})
	require.NoError(t, err, "RunWorkshopDownload")
	require.False(t, result.Success, "result = %+v, want Success=false", result)
	_, ok := result.FailedItems[333333]
	assert.True(t, ok, "expected 333333 (no result line) to be marked failed: %+v", result.FailedItems)
}

func TestRunWorkshopDownloadAuthRequired(t *testing.T) {
	script := writeFakeSteamcmdNoLogin(t, `
echo "FAILED (Invalid Password)"
`)
	result, err := RunWorkshopDownload(t.Context(), WorkshopDownloadOptions{
		Command: script, Account: "bob", WorkshopAppID: 221100, ItemIDs: []uint64{111111},
	})
	require.NoError(t, err, "RunWorkshopDownload")
	require.True(t, result.AuthRequired, "result = %+v, want AuthRequired", result)
}

func TestRunWorkshopDownloadMissingOptionsErrors(t *testing.T) {
	_, err := RunWorkshopDownload(t.Context(), WorkshopDownloadOptions{})
	require.Error(t, err)
}

func TestRunAppUpdateMissingBinaryErrors(t *testing.T) {
	_, err := RunAppUpdate(t.Context(), AppUpdateOptions{
		Command: filepath.Join(t.TempDir(), "does-not-exist"), Account: "bob", AppID: 1, InstallDir: t.TempDir(),
	})
	require.Error(t, err)
}

func TestExtractItemID(t *testing.T) {
	id, ok := extractItemID("success. downloaded item 123456 to /foo", "downloaded item")
	require.True(t, ok, "id=%d ok=%v, want 123456/true", id, ok)
	assert.Equal(t, uint64(123456), id, "id=%d ok=%v, want 123456/true", id, ok)
	_, ok = extractItemID("no marker here", "downloaded item")
	assert.False(t, ok, "expected ok=false when marker is absent")
	_, ok = extractItemID("downloaded item notanumber", "downloaded item")
	assert.False(t, ok, "expected ok=false when what follows isn't numeric")
}
