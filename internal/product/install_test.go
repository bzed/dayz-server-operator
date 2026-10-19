// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/steam"
)

func newInstaller(t testing.TB, details stubDetails) (*Installer, string) {
	t.Helper()
	script, fake := newFakeSteam(t)
	return &Installer{CacheRoot: t.TempDir(), Command: script, Account: "bob", Details: details.get}, fake
}

func calls(t testing.TB, fake string) []string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(fake, "calls"))
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// downloads counts the steamcmd runs that actually downloaded something
// (every job also runs a bare login check first).
func downloads(t testing.TB, fake string) int {
	t.Helper()
	n := 0
	for _, c := range calls(t, fake) {
		if strings.Contains(c, "workshop_download_item") || strings.Contains(c, "app_update") {
			n++
		}
	}
	return n
}

func TestInstallProduct(t *testing.T) {
	in, fake := newInstaller(t, nil)
	writeTree(t, filepath.Join(fake, "app"), map[string][]byte{"DayZServer": []byte("bin")})
	writeTree(t, fake, map[string][]byte{"buildid": []byte("1000")})
	p := config.Product{AppID: 223350, WorkshopAppID: 221100}

	r, err := in.InstallProduct(t.Context(), "dayz-stable", p, false)
	require.NoError(t, err)
	require.True(t, r.Changed)
	require.Equal(t, "1000", r.Generation)
	store := ProductStore(in.CacheRoot, "dayz-stable")
	cur, _ := store.Current()
	require.Equal(t, "1000", cur)
	b, _ := os.ReadFile(filepath.Join(store.Root, "1000", "DayZServer"))
	assert.Equal(t, "bin", string(b))

	// same build again: nothing new, and the existing generation is untouched
	err = os.WriteFile(filepath.Join(store.Root, "1000", "marker"), []byte("m"), 0o600)
	require.NoError(t, err)
	r, err = in.InstallProduct(t.Context(), "dayz-stable", p, false)
	require.NoError(t, err)
	require.False(t, r.Changed)
	_, err = os.Stat(filepath.Join(store.Root, "1000", "marker"))
	require.NoError(t, err, "an existing generation must survive a re-run untouched")

	// force stores the same build again under a new name; again and again
	for _, want := range []string{"1000-r1", "1000-r2"} {
		r, err = in.InstallProduct(t.Context(), "dayz-stable", p, true)
		require.NoError(t, err, "forced install = %+v, %v, want %s", r, err, want)
		require.Equal(t, want, r.Generation, "forced install = %+v, %v, want %s", r, err, want)
		require.True(t, r.Changed, "forced install = %+v, %v, want %s", r, err, want)
	}
	gens, _ := store.Generations()
	assert.Len(t, gens, 3)
}

func TestInstallProductErrors(t *testing.T) {
	p := config.Product{AppID: 223350}
	in, fake := newInstaller(t, nil)
	// no app dir: steamcmd never reports success
	_, err := in.InstallProduct(t.Context(), "x", p, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not report success")
	// success but no manifest
	writeTree(t, filepath.Join(fake, "app"), map[string][]byte{"f": nil})
	writeTree(t, fake, map[string][]byte{"buildid": []byte("")})
	_, err = in.InstallProduct(t.Context(), "x", p, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buildid")
	// login rejected
	auth := filepath.Join(t.TempDir(), "steamcmd")
	err = os.WriteFile(auth, []byte("#!/bin/sh\necho 'FAILED (Invalid Password)'\n"), 0o755)
	require.NoError(t, err)
	in.Command = auth
	_, err = in.InstallProduct(t.Context(), "x", p, false)
	require.ErrorIs(t, err, steam.ErrAuthRequired)
	in.Command = "/nonexistent/steamcmd"
	_, err = in.InstallProduct(t.Context(), "x", p, false)
	require.Error(t, err)
}

func TestInstallMods(t *testing.T) {
	details := stubDetails{
		1: {PublishedFileID: 1, Result: 1, TimeUpdated: 100},
		2: {PublishedFileID: 2, Result: 1, TimeUpdated: 200},
		3: {PublishedFileID: 3, Result: 1, TimeUpdated: 300},
	}
	in, fake := newInstaller(t, details)
	writeTree(t, filepath.Join(fake, "mod", "1"), modFiles(false))
	writeTree(t, filepath.Join(fake, "mod", "2"), map[string][]byte{"addons/a.pbo": buildPBO("p")}) // no meta.cpp: invalid
	// mod 3 has no fixture: steamcmd reports the download as failed
	ctx := t.Context()

	res, err := in.InstallMods(ctx, 221100, []uint64{1, 2, 3, 4}, false)
	require.NoError(t, err)
	require.True(t, res[0].Changed, "mod 1 = %+v", res[0])
	require.Equal(t, "100", res[0].Generation)
	assert.Nil(t, res[0].Err)
	require.NotNil(t, res[1].Err)
	assert.Contains(t, res[1].Err.Error(), "meta.cpp")
	require.NotNil(t, res[2].Err)
	assert.Contains(t, res[2].Err.Error(), "download failed")
	require.NotNil(t, res[3].Err)
	assert.Contains(t, res[3].Err.Error(), "does not list this item")
	cur, _ := ModStore(in.CacheRoot, 221100, 2).Current()
	assert.Empty(t, cur, "an invalid mod must not become a generation")

	// mod 1 is current: a re-run must not download again
	before := downloads(t, fake)
	res, err = in.InstallMods(ctx, 221100, []uint64{1}, false)
	require.NoError(t, err)
	require.False(t, res[0].Changed)
	require.Equal(t, "100", res[0].Generation)
	assert.Equal(t, before, downloads(t, fake), "an up-to-date mod must not be downloaded again")

	// Steam has a newer version: a new generation, the old one stays
	details[1] = FileDetails{PublishedFileID: 1, Result: 1, TimeUpdated: 150}
	res, err = in.InstallMods(ctx, 221100, []uint64{1}, false)
	require.NoError(t, err)
	require.Equal(t, "150", res[0].Generation)
	gens, _ := ModStore(in.CacheRoot, 221100, 1).Generations()
	assert.Len(t, gens, 2)

	// forced refresh: downloads although unchanged, as <time>-r<n>
	for _, want := range []string{"150-r1", "150-r2"} {
		res, err = in.InstallMods(ctx, 221100, []uint64{1}, true)
		require.NoError(t, err, "refresh = %+v, %v, want %s", res, err, want)
		require.Equal(t, want, res[0].Generation, "refresh = %+v, %v, want %s", res, err, want)
	}
	// and a normal update run afterwards sees the refreshed generation as current
	before = downloads(t, fake)
	res, _ = in.InstallMods(ctx, 221100, []uint64{1}, false)
	require.False(t, res[0].Changed, "after a refresh the mod is still up to date: %+v", res[0])
	assert.Equal(t, before, downloads(t, fake), "after a refresh the mod is still up to date: %+v", res[0])
}

// An item the public Web API does not show (result 9, unlisted or restricted) is downloaded with
// the account anyway, and its version comes from steamcmd's manifest.
func TestInstallModsUnlistedItem(t *testing.T) {
	in, fake := newInstaller(t, stubDetails{7: {PublishedFileID: 7, Result: 9}})
	writeTree(t, filepath.Join(fake, "mod", "7"), modFiles(false))
	writeTree(t, filepath.Join(fake, "timeupdated"), map[string][]byte{"7": []byte("777")})
	ctx := t.Context()

	res, err := in.InstallMods(ctx, 221100, []uint64{7}, false)
	require.NoError(t, err)
	require.Nil(t, res[0].Err)
	require.True(t, res[0].Changed)
	require.Equal(t, "777", res[0].Generation)
	// the version of an unlisted item is only known after a download: a re-run downloads again
	// and finds the generation it has
	res, err = in.InstallMods(ctx, 221100, []uint64{7}, false)
	require.NoError(t, err)
	require.Nil(t, res[0].Err)
	require.False(t, res[0].Changed)
	require.Equal(t, "777", res[0].Generation)
	// and a version bump on Steam's side becomes a new generation
	writeTree(t, filepath.Join(fake, "timeupdated"), map[string][]byte{"7": []byte("800")})
	res, err = in.InstallMods(ctx, 221100, []uint64{7}, false)
	require.NoError(t, err)
	require.Equal(t, "800", res[0].Generation)
}

func TestInstallModsRefreshWipesSteamState(t *testing.T) {
	in, fake := newInstaller(t, stubDetails{1: {PublishedFileID: 1, Result: 1, TimeUpdated: 5}})
	writeTree(t, filepath.Join(fake, "mod", "1"), modFiles(false))
	work := in.steamDir("workshop-221100")
	stale := filepath.Join(work, "steamapps", "workshop", "content", "221100", "1", "stale-file")
	writeTree(t, work, map[string][]byte{
		"steamapps/workshop/content/221100/1/stale-file": []byte("x"),
		"steamapps/workshop/downloads/221100/1/partial":  []byte("x"),
		"steamapps/workshop/temp/221100/1/partial":       []byte("x"),
		"steamapps/workshop/content/221100/2/other-mod":  []byte("keep"),
		"steamapps/workshop/appworkshop_221100.acf":      []byte("\"AppWorkshop\"\n{\n\t\"WorkshopItemsInstalled\"\n\t{\n\t\t\"1\"\n\t\t{\n\t\t\t\"size\"\t\"1\"\n\t\t}\n\t\t\"2\"\n\t\t{\n\t\t\t\"size\"\t\"2\"\n\t\t}\n\t}\n}\n"),
	})
	_, err := in.InstallMods(t.Context(), 221100, []uint64{1}, true)
	require.NoError(t, err)
	_, err = os.Stat(stale)
	assert.True(t, os.IsNotExist(err), "steamcmd's old copy of the item must be wiped before a forced download")
	_, err = os.Stat(filepath.Join(work, "steamapps", "workshop", "content", "221100", "2", "other-mod"))
	require.NoError(t, err, "other items must not be touched")
	acf, _ := os.ReadFile(filepath.Join(work, "steamapps", "workshop", "appworkshop_221100.acf"))
	require.NotContains(t, string(acf), `"1"`)
	assert.Contains(t, string(acf), `"2"`)
}

func TestInstallModsErrors(t *testing.T) {
	ctx := t.Context()
	in, _ := newInstaller(t, stubDetails{1: {PublishedFileID: 1, Result: 1, TimeUpdated: 5}})
	in.Command = "/nonexistent/steamcmd"
	_, err := in.InstallMods(ctx, 221100, []uint64{1}, false)
	require.Error(t, err)
	in.Details = failingDetails
	_, err = in.InstallMods(ctx, 221100, []uint64{1}, false)
	require.Error(t, err, "a failing Steam Web API must fail")
	// nothing stale, nothing to do
	in, _ = newInstaller(t, stubDetails{})
	res, err := in.InstallMods(ctx, 221100, nil, false)
	require.NoError(t, err)
	assert.Empty(t, res)
	// auth
	auth := filepath.Join(t.TempDir(), "steamcmd")
	err = os.WriteFile(auth, []byte("#!/bin/sh\necho 'FAILED (Invalid Password)'\n"), 0o755)
	require.NoError(t, err)
	in, _ = newInstaller(t, stubDetails{1: {PublishedFileID: 1, Result: 1, TimeUpdated: 5}})
	in.Command = auth
	_, err = in.InstallMods(ctx, 221100, []uint64{1}, false)
	require.ErrorIs(t, err, steam.ErrAuthRequired)
}

func failingDetails(context.Context, []uint64) (map[uint64]FileDetails, error) {
	return nil, errors.New("api down")
}

func TestInstallModsBatchesDetailsLookups(t *testing.T) {
	details := stubDetails{}
	var ids []uint64
	for i := uint64(1); i <= 150; i++ {
		details[i] = FileDetails{PublishedFileID: i, Result: 1, TimeUpdated: 7}
		ids = append(ids, i)
	}
	in, fake := newInstaller(t, details)
	for _, id := range ids {
		writeTree(t, filepath.Join(fake, "mod", strconv.FormatUint(id, 10)), modFiles(false))
	}
	res, err := in.InstallMods(t.Context(), 221100, ids, false)
	require.NoError(t, err)
	for _, r := range res {
		require.Nil(t, r.Err)
		require.True(t, r.Changed, "result %+v", r)
	}
}

func TestDropVDFBlockAndBaseGeneration(t *testing.T) {
	in := "a\n\t\"7\"\n\t{\n\t\t\"x\"\n\t\t{\n\t\t}\n\t}\nb\n\t\"7\"\n\t{\n\t}\nc"
	got := dropVDFBlock(in, "7")
	assert.Equal(t, "a\nb\nc", got)
	got = dropVDFBlock("no block here", "7")
	assert.Equal(t, "no block here", got)
	for in, want := range map[string]string{"100": "100", "100-r3": "100", "-r1": "-r1", "abc-rx": "abc"} {
		got := baseGeneration(in)
		assert.Equal(t, want, got)
	}
	require.Equal(t, "a\nb", tail("a\nb"))
	assert.Equal(t, 14, strings.Count(tail(strings.Repeat("x\n", 40)), "\n"))
}
