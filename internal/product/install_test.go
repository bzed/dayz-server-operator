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

	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/steam"
)

func newInstaller(t *testing.T, details stubDetails) (*Installer, string) {
	t.Helper()
	script, fake := newFakeSteam(t)
	return &Installer{CacheRoot: t.TempDir(), Command: script, Account: "bob", Details: details.get}, fake
}

func calls(t *testing.T, fake string) []string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(fake, "calls"))
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// downloads counts the steamcmd runs that actually downloaded something
// (every job also runs a bare login check first).
func downloads(t *testing.T, fake string) int {
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

	r, err := in.InstallProduct(context.Background(), "dayz-stable", p, false)
	if err != nil || !r.Changed || r.Generation != "1000" {
		t.Fatalf("first install = %+v, %v", r, err)
	}
	store := ProductStore(in.CacheRoot, "dayz-stable")
	if cur, _ := store.Current(); cur != "1000" {
		t.Fatalf("current = %q", cur)
	}
	if b, _ := os.ReadFile(filepath.Join(store.Root, "1000", "DayZServer")); string(b) != "bin" {
		t.Errorf("generation content = %q", b)
	}

	// same build again: nothing new, and the existing generation is untouched
	if err := os.WriteFile(filepath.Join(store.Root, "1000", "marker"), []byte("m"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err = in.InstallProduct(context.Background(), "dayz-stable", p, false); err != nil || r.Changed {
		t.Fatalf("second install = %+v, %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "1000", "marker")); err != nil {
		t.Error("an existing generation must survive a re-run untouched")
	}

	// force stores the same build again under a new name; again and again
	for _, want := range []string{"1000-r1", "1000-r2"} {
		if r, err = in.InstallProduct(context.Background(), "dayz-stable", p, true); err != nil || r.Generation != want || !r.Changed {
			t.Fatalf("forced install = %+v, %v, want %s", r, err, want)
		}
	}
	if gens, _ := store.Generations(); len(gens) != 3 {
		t.Errorf("generations = %v, want 3", gens)
	}
}

func TestInstallProductErrors(t *testing.T) {
	p := config.Product{AppID: 223350}
	in, fake := newInstaller(t, nil)
	// no app dir: steamcmd never reports success
	if _, err := in.InstallProduct(context.Background(), "x", p, false); err == nil || !strings.Contains(err.Error(), "did not report success") {
		t.Errorf("err = %v", err)
	}
	// success but no manifest
	writeTree(t, filepath.Join(fake, "app"), map[string][]byte{"f": nil})
	writeTree(t, fake, map[string][]byte{"buildid": []byte("")})
	if _, err := in.InstallProduct(context.Background(), "x", p, false); err == nil || !strings.Contains(err.Error(), "buildid") {
		t.Errorf("err = %v", err)
	}
	// login rejected
	auth := filepath.Join(t.TempDir(), "steamcmd")
	if err := os.WriteFile(auth, []byte("#!/bin/sh\necho 'FAILED (Invalid Password)'\n"), 0o755); err != nil { //nolint:gosec // fixture
		t.Fatal(err)
	}
	in.Command = auth
	if _, err := in.InstallProduct(context.Background(), "x", p, false); !errors.Is(err, steam.ErrAuthRequired) {
		t.Errorf("err = %v, want ErrAuthRequired", err)
	}
	in.Command = "/nonexistent/steamcmd"
	if _, err := in.InstallProduct(context.Background(), "x", p, false); err == nil {
		t.Error("a missing steamcmd must fail")
	}
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
	ctx := context.Background()

	res, err := in.InstallMods(ctx, 221100, []uint64{1, 2, 3, 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].Changed || res[0].Generation != "100" || res[0].Err != nil {
		t.Errorf("mod 1 = %+v", res[0])
	}
	if res[1].Err == nil || !strings.Contains(res[1].Err.Error(), "meta.cpp") {
		t.Errorf("mod 2 = %+v, want a validation error", res[1])
	}
	if res[2].Err == nil || !strings.Contains(res[2].Err.Error(), "download failed") {
		t.Errorf("mod 3 = %+v, want a download error", res[2])
	}
	if res[3].Err == nil || !strings.Contains(res[3].Err.Error(), "does not list this item") {
		t.Errorf("mod 4 = %+v, want an unknown-item error", res[3])
	}
	if cur, _ := ModStore(in.CacheRoot, 221100, 2).Current(); cur != "" {
		t.Error("an invalid mod must not become a generation")
	}

	// mod 1 is current: a re-run must not download again
	before := downloads(t, fake)
	if res, err = in.InstallMods(ctx, 221100, []uint64{1}, false); err != nil || res[0].Changed || res[0].Generation != "100" {
		t.Fatalf("re-run = %+v, %v", res, err)
	}
	if downloads(t, fake) != before {
		t.Error("an up-to-date mod must not be downloaded again")
	}

	// Steam has a newer version: a new generation, the old one stays
	details[1] = FileDetails{PublishedFileID: 1, Result: 1, TimeUpdated: 150}
	if res, err = in.InstallMods(ctx, 221100, []uint64{1}, false); err != nil || res[0].Generation != "150" {
		t.Fatalf("update = %+v, %v", res, err)
	}
	if gens, _ := ModStore(in.CacheRoot, 221100, 1).Generations(); len(gens) != 2 {
		t.Errorf("generations = %v, want the old one kept", gens)
	}

	// forced refresh: downloads although unchanged, as <time>-r<n>
	for _, want := range []string{"150-r1", "150-r2"} {
		if res, err = in.InstallMods(ctx, 221100, []uint64{1}, true); err != nil || res[0].Generation != want {
			t.Fatalf("refresh = %+v, %v, want %s", res, err, want)
		}
	}
	// and a normal update run afterwards sees the refreshed generation as current
	before = downloads(t, fake)
	if res, _ = in.InstallMods(ctx, 221100, []uint64{1}, false); res[0].Changed || downloads(t, fake) != before {
		t.Errorf("after a refresh the mod is still up to date: %+v", res[0])
	}
}

// An item the public Web API does not show (result 9, unlisted or restricted) is downloaded with
// the account anyway, and its version comes from steamcmd's manifest.
func TestInstallModsUnlistedItem(t *testing.T) {
	in, fake := newInstaller(t, stubDetails{7: {PublishedFileID: 7, Result: 9}})
	writeTree(t, filepath.Join(fake, "mod", "7"), modFiles(false))
	writeTree(t, filepath.Join(fake, "timeupdated"), map[string][]byte{"7": []byte("777")})
	ctx := context.Background()

	res, err := in.InstallMods(ctx, 221100, []uint64{7}, false)
	if err != nil || res[0].Err != nil || !res[0].Changed || res[0].Generation != "777" {
		t.Fatalf("install = %+v, %v", res, err)
	}
	// the version of an unlisted item is only known after a download: a re-run downloads again
	// and finds the generation it has
	res, err = in.InstallMods(ctx, 221100, []uint64{7}, false)
	if err != nil || res[0].Err != nil || res[0].Changed || res[0].Generation != "777" {
		t.Fatalf("re-run = %+v, %v", res, err)
	}
	// and a version bump on Steam's side becomes a new generation
	writeTree(t, filepath.Join(fake, "timeupdated"), map[string][]byte{"7": []byte("800")})
	if res, err = in.InstallMods(ctx, 221100, []uint64{7}, false); err != nil || res[0].Generation != "800" {
		t.Fatalf("update = %+v, %v", res, err)
	}
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
	if _, err := in.InstallMods(context.Background(), 221100, []uint64{1}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("steamcmd's old copy of the item must be wiped before a forced download")
	}
	if _, err := os.Stat(filepath.Join(work, "steamapps", "workshop", "content", "221100", "2", "other-mod")); err != nil {
		t.Error("other items must not be touched")
	}
	acf, _ := os.ReadFile(filepath.Join(work, "steamapps", "workshop", "appworkshop_221100.acf"))
	if strings.Contains(string(acf), `"1"`) || !strings.Contains(string(acf), `"2"`) {
		t.Errorf("acf = %s", acf)
	}
}

func TestInstallModsErrors(t *testing.T) {
	ctx := context.Background()
	in, _ := newInstaller(t, stubDetails{1: {PublishedFileID: 1, Result: 1, TimeUpdated: 5}})
	in.Command = "/nonexistent/steamcmd"
	if _, err := in.InstallMods(ctx, 221100, []uint64{1}, false); err == nil {
		t.Error("a missing steamcmd must fail")
	}
	in.Details = failingDetails
	if _, err := in.InstallMods(ctx, 221100, []uint64{1}, false); err == nil {
		t.Error("a failing Steam Web API must fail")
	}
	// nothing stale, nothing to do
	in, _ = newInstaller(t, stubDetails{})
	res, err := in.InstallMods(ctx, 221100, nil, false)
	if err != nil || len(res) != 0 {
		t.Errorf("empty = %v, %v", res, err)
	}
	// auth
	auth := filepath.Join(t.TempDir(), "steamcmd")
	if err := os.WriteFile(auth, []byte("#!/bin/sh\necho 'FAILED (Invalid Password)'\n"), 0o755); err != nil { //nolint:gosec // fixture
		t.Fatal(err)
	}
	in, _ = newInstaller(t, stubDetails{1: {PublishedFileID: 1, Result: 1, TimeUpdated: 5}})
	in.Command = auth
	if _, err := in.InstallMods(ctx, 221100, []uint64{1}, false); !errors.Is(err, steam.ErrAuthRequired) {
		t.Errorf("err = %v, want ErrAuthRequired", err)
	}
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
	res, err := in.InstallMods(context.Background(), 221100, ids, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Err != nil || !r.Changed {
			t.Fatalf("result %+v", r)
		}
	}
}

func TestDropVDFBlockAndBaseGeneration(t *testing.T) {
	in := "a\n\t\"7\"\n\t{\n\t\t\"x\"\n\t\t{\n\t\t}\n\t}\nb\n\t\"7\"\n\t{\n\t}\nc"
	if got := dropVDFBlock(in, "7"); got != "a\nb\nc" {
		t.Errorf("dropVDFBlock = %q", got)
	}
	if got := dropVDFBlock("no block here", "7"); got != "no block here" {
		t.Errorf("dropVDFBlock = %q", got)
	}
	for in, want := range map[string]string{"100": "100", "100-r3": "100", "-r1": "-r1", "abc-rx": "abc"} {
		if got := baseGeneration(in); got != want {
			t.Errorf("baseGeneration(%q) = %q, want %q", in, got, want)
		}
	}
	if tail("a\nb") != "a\nb" || strings.Count(tail(strings.Repeat("x\n", 40)), "\n") != 14 {
		t.Error("tail keeps the last 15 lines")
	}
}
