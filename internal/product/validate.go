// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bzed/dayz-server-operator/internal/moddeps"
)

// ValidateMod checks a downloaded or imported mod directory before it may
// become a generation (§C7): every PBO under addons/ must parse. A workshop
// mod must have a meta.cpp; a local mod must have at least one PBO, and each
// must carry a prefix header (a PBO without one loads without error, but none
// of its scripts are loaded, §C22). A workshop mod's PBO without a prefix is
// allowed: a third-party PBO is not ours to reject.
func ValidateMod(dir string, local bool) error {
	if !local {
		if _, err := os.Stat(filepath.Join(dir, "meta.cpp")); err != nil {
			return fmt.Errorf("product: %s: meta.cpp missing", dir)
		}
	}
	pbos, err := filepath.Glob(filepath.Join(dir, "addons", "*"))
	if err != nil {
		return err
	}
	n := 0
	for _, p := range pbos {
		if !strings.EqualFold(filepath.Ext(p), ".pbo") {
			continue
		}
		n++
		prefix, err := pboPrefix(p)
		if err != nil {
			return fmt.Errorf("product: %s: %w", p, err)
		}
		if local && prefix == "" {
			return fmt.Errorf("product: %s: PBO has no prefix header", p)
		}
	}
	if local && n == 0 {
		return fmt.Errorf("product: %s: addons/ holds no .pbo", dir)
	}
	return nil
}

// pboPrefix parses the PBO's header table (mapping the file rather than
// reading it, since mod PBOs can be very large) and returns its prefix.
func pboPrefix(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is under a mod dir dzo just downloaded or imported
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.Size() == 0 {
		return "", fmt.Errorf("empty PBO")
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(fi.Size()), syscall.PROT_READ, syscall.MAP_SHARED) //nolint:gosec // G115: size of a file that fits in memory
	if err != nil {
		return "", err
	}
	defer func() { _ = syscall.Munmap(data) }()
	pbo, err := moddeps.OpenPBO(data)
	if err != nil {
		return "", err
	}
	return pbo.Prefix, nil
}

// ValidateSigned checks a debug client mod (site.ModRef.DebugClient): it ships at least one .bikey
// under keys/, and every PBO under addons/ has a <pbo>.<authority>.bisign next to it whose authority
// is one of those keys. It looks at the files only; the server verifies the signatures themselves
// and kicks a client whose PBOs do not match.
func ValidateSigned(dir string) error {
	authorities := map[string]bool{}
	for _, sub := range []string{"keys", "Keys", "key", "Key"} {
		keys, err := filepath.Glob(filepath.Join(dir, sub, "*.bikey"))
		if err != nil {
			return err
		}
		for _, k := range keys {
			authorities[strings.ToLower(strings.TrimSuffix(filepath.Base(k), filepath.Ext(k)))] = true
		}
	}
	if len(authorities) == 0 {
		return fmt.Errorf("product: %s: a signed client mod needs its public key as keys/<authority>.bikey", dir)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "addons"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".pbo") {
			continue
		}
		prefix := strings.ToLower(e.Name()) + "."
		ok := false
		for _, o := range entries {
			n := strings.ToLower(o.Name())
			if strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".bisign") && authorities[strings.TrimSuffix(strings.TrimPrefix(n, prefix), ".bisign")] {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("product: %s: no %s.<authority>.bisign made with a key in keys/ (sign it, for example with armake2 or the dayz-dev skill's dayz-mod-pack.sh)", filepath.Join(dir, "addons", e.Name()), e.Name())
		}
	}
	return nil
}
