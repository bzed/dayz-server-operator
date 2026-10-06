// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runfiles renders the files an instance needs to start besides its
// mission: the signature keys, serverDZ.cfg with what dzo owns enforced, and
// the BattlEye seed config with the RCon port and password (§C6 steps 2, 6
// and 7). `dzo instance render` writes them into the instance, and the boot
// test writes the same into a disposable server tree.
package runfiles

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bzed/dayz-server-operator/internal/servercfg"
)

// Keys returns the .bikey files a start needs: those of the server build and
// of every mod the clients load. Servermods contribute none, nothing verifies
// their signatures. The directory may be called keys, Keys or key.
func Keys(productDir string, clientModDirs []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, dir := range append([]string{productDir}, clientModDirs...) {
		for _, sub := range []string{"keys", "Keys", "key", "Key"} {
			files, err := filepath.Glob(filepath.Join(dir, sub, "*.bikey"))
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				if name := strings.ToLower(filepath.Base(f)); !seen[name] {
					seen[name] = true
					out = append(out, f)
				}
			}
		}
	}
	return out, nil
}

// ServerCfg parses the site's serverDZ.cfg and sets what dzo owns: the mission
// template (Missions/DayZ/template, it must match the mission dir) and the Steam query port (the
// health probe and the monitoring ask it). Everything else stays as written.
func ServerCfg(src []byte, template string, queryPort int) (*servercfg.File, error) {
	f, err := servercfg.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("serverDZ.cfg: %w", err)
	}
	f.SetPathScalar([]string{"Missions", "DayZ"}, "template", template, true)
	if queryPort != 0 {
		f.SetScalar("steamQueryPort", strconv.Itoa(queryPort), false)
	}
	return f, nil
}

// StdinAnswer is what the server reads from its stdin: "Ignore" to an assertion prompt.
const StdinAnswer = "i\n"

// BattlEyeCfg is the seed for profiles/battleye/beserver_x64.cfg. The server
// reads it once and keeps working in a beserver_x64_active_<hex>.cfg beside it.
// ip is the address RCon listens on; "" leaves it to BattlEye (every interface).
func BattlEyeCfg(password string, port int, ip string) string {
	s := fmt.Sprintf("RConPassword %s\nRestrictRCon 0\nRConPort %d\n", password, port)
	if ip != "" {
		s += "RConIP " + ip + "\n"
	}
	return s
}

// RandomPassword returns n random letters and digits.
func RandomPassword(n int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

// RConPassword returns the instance's RCon password from secretsDir/rcon/<name>,
// creating it on first use.
func RConPassword(secretsDir, name string) (string, error) {
	path := filepath.Join(secretsDir, "rcon", name)
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // dzo's own secrets directory
		if pw := strings.TrimSpace(string(b)); pw != "" {
			return pw, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	pw, err := RandomPassword(24)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return pw, os.WriteFile(path, []byte(pw+"\n"), 0o600)
}

// Input is everything Write needs for one instance.
type Input struct {
	RuntimeDir  string // the instance's runtime/
	ProfilesDir string // the instance's profiles/
	StorageDir  string // the instance's storage/<map>, created if missing; empty skips it
	ServerCfg   []byte // the site's serverDZ.cfg
	Template    string
	QueryPort   int
	RConPort    int
	RConIP      string // address RCon listens on, "" for every interface
	RConPass    string
	Keys        []string
}

// Write renders runtime/keys, runtime/serverDZ.cfg and the BattlEye seed
// config. The keys are copied, not linked: they are mounted into the
// container, where a link into the cache would not resolve. Stale BattlEye
// working copies are removed so that the seed's port takes effect.
func Write(in Input) error {
	cfg, err := ServerCfg(in.ServerCfg, in.Template, in.QueryPort)
	if err != nil {
		return err
	}
	keys := filepath.Join(in.RuntimeDir, "keys")
	if err := os.RemoveAll(keys); err != nil {
		return err
	}
	if err := os.MkdirAll(keys, 0o750); err != nil {
		return err
	}
	for _, k := range in.Keys {
		b, err := os.ReadFile(k) //nolint:gosec // a key found by Keys
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(keys, filepath.Base(k)), b, 0o640); err != nil { //nolint:gosec // public keys
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(in.RuntimeDir, "serverDZ.cfg"), cfg.Bytes(), 0o600); err != nil {
		return err
	}
	// The server's stdin (mounted at /stdin): the answer "Ignore" to the assertion prompt that
	// the experimental builds show at shutdown ("Script is leaking!", (A)bort (R)etry (I)gnore).
	if err := os.WriteFile(filepath.Join(in.RuntimeDir, "stdin"), []byte(StdinAnswer), 0o640); err != nil { //nolint:gosec // read by the container's root
		return err
	}
	if in.StorageDir != "" {
		if err := os.MkdirAll(in.StorageDir, 0o750); err != nil {
			return err
		}
	}
	be := filepath.Join(in.ProfilesDir, "battleye")
	if err := os.MkdirAll(be, 0o750); err != nil {
		return err
	}
	stale, _ := filepath.Glob(filepath.Join(be, "beserver_x64_active_*.cfg"))
	for _, s := range stale {
		_ = os.Remove(s)
	}
	return os.WriteFile(filepath.Join(be, "beserver_x64.cfg"), []byte(BattlEyeCfg(in.RConPass, in.RConPort, in.RConIP)), 0o600)
}

// Hex is a short random identifier.
func Hex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
