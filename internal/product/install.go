// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/bzed/dayz-server-operator/internal/cache"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/steam"
)

// Installer turns steamcmd downloads and local mod sources into immutable
// cache generations (§C7). Nothing here ever changes a generation that
// exists: a new state is a new generation, and "current" is switched last.
type Installer struct {
	CacheRoot string
	Command   string // steamcmd binary; "" means "steamcmd"
	Account   string
	// Details looks up Steam file details (FileDetailsClient.GetFileDetails in production).
	Details func(ctx context.Context, ids []uint64) (map[uint64]FileDetails, error)
}

// InstallResult is the outcome for one product or mod.
type InstallResult struct {
	ID         uint64 // workshop id; 0 for a product or local mod
	Generation string // the generation now current ("" if it failed)
	Changed    bool   // a new generation was created
	Err        error
}

// steamDir is a steamcmd working dir (mutable, never mounted into servers).
func (in *Installer) steamDir(name string) string {
	return filepath.Join(in.CacheRoot, "steamcmd", name)
}

// lockSteam serialises steamcmd jobs across processes (§C7).
func (in *Installer) lockSteam() (func(), error) {
	dir := filepath.Join(in.CacheRoot, "steamcmd")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("product: %w", err)
	}
	return cache.Flock(filepath.Join(dir, ".lock"))
}

func authErr(r JobResult) error {
	if r.AuthRequired {
		return fmt.Errorf("%w: run `dzo steam login`", steam.ErrAuthRequired)
	}
	return nil
}

var buildIDRe = regexp.MustCompile(`"buildid"\s+"(\d+)"`)

// InstallProduct downloads the product's server build with steamcmd
// (app_update validate) and stores it as a generation named after its Steam
// build id, then makes it current. With force, an already-stored build is
// stored again as <buildid>-r<n> (the server-side equivalent of the forced
// mod refresh). It never runs on its own: only `dzo product install|update`
// calls it (D7).
func (in *Installer) InstallProduct(ctx context.Context, name string, p config.Product, force bool) (InstallResult, error) {
	unlock, err := in.lockSteam()
	if err != nil {
		return InstallResult{}, err
	}
	defer unlock()

	work := in.steamDir(name)
	if err := os.MkdirAll(work, 0o750); err != nil {
		return InstallResult{}, fmt.Errorf("product: %w", err)
	}
	res, err := RunAppUpdate(ctx, AppUpdateOptions{
		Command: in.Command, Account: in.Account, AppID: p.AppID, InstallDir: work, BetaBranch: p.BetaBranch, Validate: true,
	})
	if err != nil {
		return InstallResult{}, err
	}
	if err := authErr(res); err != nil {
		return InstallResult{}, err
	}
	if !res.Success {
		return InstallResult{}, fmt.Errorf("product: app_update %d did not report success:\n%s", p.AppID, tail(res.Log))
	}
	manifest, err := os.ReadFile(filepath.Join(work, "steamapps", fmt.Sprintf("appmanifest_%d.acf", p.AppID))) //nolint:gosec // path built from a configured app id
	if err != nil {
		return InstallResult{}, fmt.Errorf("product: read build id: %w", err)
	}
	m := buildIDRe.FindSubmatch(manifest)
	if m == nil {
		return InstallResult{}, fmt.Errorf("product: no buildid in the app manifest of %s", name)
	}
	return in.publish(ProductStore(in.CacheRoot, name), string(m[1]), force, work)
}

// publish stores src as generation id of store (as id-r<n> when force and id
// exists) and makes it current. An existing generation is never touched.
func (in *Installer) publish(store *cache.Store, id string, force bool, src string) (InstallResult, error) {
	if force {
		id = nextRefreshID(store, id)
	}
	created, err := in.stage(store, id, src)
	if err != nil {
		return InstallResult{}, err
	}
	if err := store.SetCurrent(id); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Generation: id, Changed: created}, nil
}

// stage copies src into a scratch dir and renames it to <store>/<id>, so the
// generation appears complete or not at all. It reports false when the
// generation already existed (which is left alone).
func (in *Installer) stage(store *cache.Store, id, src string) (bool, error) {
	scratch, err := in.scratch()
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(scratch) //nolint:errcheck // gone after a successful rename
	if err := cache.CopyGeneration(src, scratch); err != nil {
		return false, err
	}
	return installGeneration(store, id, scratch)
}

func (in *Installer) scratch() (string, error) {
	tmp := filepath.Join(in.CacheRoot, "tmp")
	if err := os.MkdirAll(tmp, 0o750); err != nil {
		return "", fmt.Errorf("product: %w", err)
	}
	return os.MkdirTemp(tmp, "gen-")
}

// installGeneration renames a finished scratch dir into place.
func installGeneration(store *cache.Store, id, scratch string) (bool, error) {
	if err := store.EnsureRoot(); err != nil {
		return false, err
	}
	err := os.Rename(scratch, filepath.Join(store.Root, id)) //nolint:gosec // scratch is our own dir, id a generation id
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST):
		return false, nil
	}
	return false, fmt.Errorf("product: install generation %s: %w", id, err)
}

// nextRefreshID returns the first free "<base>-r<n>", n >= 1.
func nextRefreshID(store *cache.Store, base string) string {
	for n := 1; ; n++ {
		id := base + "-r" + strconv.Itoa(n)
		if _, err := os.Lstat(filepath.Join(store.Root, id)); err != nil { //nolint:gosec // id is base plus a counter
			return id
		}
	}
}

// baseGeneration strips a "-r<n>" refresh suffix.
func baseGeneration(id string) string {
	if i := strings.LastIndex(id, "-r"); i > 0 {
		return id[:i]
	}
	return id
}

func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	return strings.Join(lines, "\n")
}

// InstallMods brings the given workshop mods (of one workshop app) up to
// date: a mod whose current generation matches Steam's time_updated is left
// alone, unless force. Everything stale is downloaded in one steamcmd job,
// validated and stored as a new generation; one mod failing does not stop the
// others. force also wipes steamcmd's own state for the item first (the
// forced refresh, for a bugged steamcmd cache, §C7).
func (in *Installer) InstallMods(ctx context.Context, workshopApp uint32, ids []uint64, force bool) ([]InstallResult, error) {
	details := map[uint64]FileDetails{}
	for start := 0; start < len(ids); start += 100 {
		d, err := in.Details(ctx, ids[start:min(start+100, len(ids))])
		if err != nil {
			return nil, err
		}
		for id, fd := range d {
			details[id] = fd
		}
	}

	results := make([]InstallResult, len(ids))
	var todo []uint64
	for i, id := range ids {
		results[i].ID = id
		fd, ok := details[id]
		if !ok || fd.Result != 1 {
			results[i].Err = fmt.Errorf("workshop item %d: Steam has no such item (result %d)", id, fd.Result)
			continue
		}
		cur, err := ModStore(in.CacheRoot, workshopApp, id).Current()
		if err != nil {
			results[i].Err = err
			continue
		}
		if !force && cur != "" && baseGeneration(cur) == ModGenerationID(fd.TimeUpdated, 0) {
			results[i].Generation = cur
			continue
		}
		todo = append(todo, id)
	}
	if len(todo) == 0 {
		return results, nil
	}

	unlock, err := in.lockSteam()
	if err != nil {
		return nil, err
	}
	defer unlock()
	work := in.steamDir("workshop-" + strconv.FormatUint(uint64(workshopApp), 10))
	if err := os.MkdirAll(work, 0o750); err != nil {
		return nil, fmt.Errorf("product: %w", err)
	}
	if force {
		for _, id := range todo {
			if err := wipeWorkshopItem(work, workshopApp, id); err != nil {
				return nil, err
			}
		}
	}
	job, err := RunWorkshopDownload(ctx, WorkshopDownloadOptions{
		Command: in.Command, Account: in.Account, WorkshopAppID: workshopApp, ItemIDs: todo, InstallDir: work, Validate: true,
	})
	if err != nil {
		return nil, err
	}
	if err := authErr(job); err != nil {
		return nil, err
	}

	wanted := map[uint64]bool{}
	for _, id := range todo {
		wanted[id] = true
	}
	for i, id := range ids {
		if !wanted[id] {
			continue
		}
		if msg, failed := job.FailedItems[id]; failed {
			results[i].Err = fmt.Errorf("workshop item %d: download failed: %s", id, msg)
			continue
		}
		src := filepath.Join(work, "steamapps", "workshop", "content", strconv.FormatUint(uint64(workshopApp), 10), strconv.FormatUint(id, 10))
		if err := ValidateMod(src, false); err != nil {
			results[i].Err = err
			continue
		}
		r, err := in.publish(ModStore(in.CacheRoot, workshopApp, id), ModGenerationID(details[id].TimeUpdated, 0), force, src)
		r.ID, r.Err = id, err
		results[i] = r
	}
	return results, nil
}

// wipeWorkshopItem removes steamcmd's downloaded copy, download leftovers and
// manifest entry of one item from the steamcmd work dir (never from a cache
// generation), so the next download starts clean.
func wipeWorkshopItem(work string, app uint32, id uint64) error {
	a, i := strconv.FormatUint(uint64(app), 10), strconv.FormatUint(id, 10)
	for _, p := range []string{
		filepath.Join(work, "steamapps", "workshop", "content", a, i),
		filepath.Join(work, "steamapps", "workshop", "downloads", a, i),
		filepath.Join(work, "steamapps", "workshop", "temp", a, i),
	} {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("product: %w", err)
		}
	}
	acf := filepath.Join(work, "steamapps", "workshop", "appworkshop_"+a+".acf")
	data, err := os.ReadFile(acf) //nolint:gosec // path built from a configured app id
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("product: %w", err)
	}
	return os.WriteFile(acf, []byte(dropVDFBlock(string(data), i)), 0o600)
}

// dropVDFBlock removes every `"<key>" { ... }` block from a Valve KeyValues
// (.acf) text: the item's entries under WorkshopItemsInstalled and
// WorkshopItemDetails.
func dropVDFBlock(text, key string) string {
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != `"`+key+`"` || i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) != "{" {
			out = append(out, lines[i])
			continue
		}
		for depth := 0; i+1 < len(lines); {
			i++
			switch strings.TrimSpace(lines[i]) {
			case "{":
				depth++
			case "}":
				depth--
			}
			if depth == 0 {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}
