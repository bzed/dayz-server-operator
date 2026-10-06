// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package boottest is `dzo test boot` (§C22): it boots a real DayZServer, from
// a disposable tree, on what a render produced, and says whether the game
// accepted it. It is a development tool. It never runs next to live servers
// (see Guard), and it never writes into the server install, the mods or any
// cache: everything it writes is in its own tree.
package boottest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bzed/dayz-server-operator/internal/a2s"
	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/runfiles"
)

// Mod is one mod of the test: its directory name in the tree (@Name) and where
// it lives.
type Mod struct {
	Name   string
	Dir    string
	Server bool // -servermod instead of -mod
}

var modName = regexp.MustCompile(`^@[A-Za-z0-9_.-]+$`)

// Config describes one boot.
type Config struct {
	ServerDir  string // the server install; only read
	TreeDir    string // where the tree is built; created
	Template   string // mission directory name, from serverDZ.cfg
	MissionDir string // the staged mission, copied into the tree
	ServerCfg  []byte // serverDZ.cfg, rewritten for the test
	Keys       []string
	Mods       []Mod
	Params     []string // extra DayZServer arguments
	Admin      *admin.ModConfig
	Timeout    time.Duration // until the server answers; default 3 minutes
	Settle     time.Duration // after that, for init scripts and the mod's first contact; default 15 s
	Expect     []string
	Baseline   *Baseline
	ModScripts map[string]int
	Log        io.Writer
	// Image runs the server in this container image instead of natively (the image the units
	// use). The tree, the server install and the mods are mounted at their own paths, the
	// install and the mods as overlays, so the tree's links resolve and nothing is written
	// into them.
	Image string
	// PortRange is [from, to) for the three test ports; the default is 20000-60000.
	PortRange [2]int
	// Podman is the podman binary (default "podman").
	Podman string
}

// Result is the outcome of a boot.
type Result struct {
	Checks   []Check
	Profiles string // the tree's profiles directory
	Ports    Ports
}

// Ports are the three ports of a test server.
type Ports struct{ Game, Query, RCon int }

// Args builds the DayZServer command line. Mod paths are relative to the tree:
// the engine silently ignores absolute ones.
func Args(game int, mods []Mod, extra []string) ([]string, error) {
	args := []string{
		"-config=serverDZ.cfg", "-profiles=profiles", "-port=" + strconv.Itoa(game), "-BEpath=profiles/battleye",
		"-nosplash", "-nopause", "-dologs", "-adminlog",
	}
	var client, server []string
	for _, m := range mods {
		if !modName.MatchString(m.Name) {
			return nil, fmt.Errorf("mod %q: the name must be @<name>; absolute or nested paths are silently ignored by the server", m.Name)
		}
		if m.Server {
			server = append(server, m.Name)
		} else {
			client = append(client, m.Name)
		}
	}
	if len(client) > 0 {
		args = append(args, "-mod="+strings.Join(client, ";"))
	}
	if len(server) > 0 {
		args = append(args, "-servermod="+strings.Join(server, ";"))
	}
	return append(args, extra...), nil
}

// FreePorts returns n ports that are free for UDP and TCP right now, apart
// from each other by at least 10 (the server uses the ports next to its game
// port), and not any default of the game, Steam query or RCon.
func FreePorts(n int) ([]int, error) { return FreePortsIn(n, 20000, 60000) }

// FreePortsIn is FreePorts within [from, to): a host that reserves a port range for the game
// says which.
func FreePortsIn(n, from, to int) ([]int, error) {
	var out []int
	base := from + int(time.Now().UnixNano()/1000)%max(to-from-100, 1)
	for p := base; len(out) < n && p < to; p += 17 {
		if (p >= 2300 && p <= 2310) || (p >= 27015 && p <= 27017) || !free(p) {
			continue
		}
		out = append(out, p)
	}
	if len(out) < n {
		return nil, errors.New("boottest: no free ports")
	}
	return out, nil
}

func free(p int) bool {
	u, err := net.ListenPacket("udp", ":"+strconv.Itoa(p))
	if err != nil {
		return false
	}
	_ = u.Close()
	t, err := net.Listen("tcp", ":"+strconv.Itoa(p))
	if err != nil {
		return false
	}
	_ = t.Close()
	return true
}

var stopGrace = 15 * time.Second // a variable so that tests need not wait

// Run builds the tree, boots the server, waits until it answers the Steam
// query, lets it settle, stops it and evaluates the logs. The error is for
// problems of the test itself (no server, no port, cannot start); what the
// server did wrong is in the checks.
func Run(ctx context.Context, c Config) (*Result, error) {
	if c.Timeout == 0 {
		c.Timeout = 3 * time.Minute
	}
	if c.Settle == 0 {
		c.Settle = 20 * time.Second
	}
	logf := func(format string, a ...any) {
		if c.Log != nil {
			_, _ = fmt.Fprintf(c.Log, format+"\n", a...)
		}
	}
	from, to := c.PortRange[0], c.PortRange[1]
	if to <= from {
		from, to = 20000, 60000
	}
	ps, err := FreePortsIn(3, from, to)
	if err != nil {
		return nil, err
	}
	ports := Ports{Game: ps[0], Query: ps[1], RCon: ps[2]}
	args, err := Args(ports.Game, c.Mods, c.Params)
	if err != nil {
		return nil, err
	}
	if err := BuildTree(c, ports); err != nil {
		return nil, err
	}
	profiles := filepath.Join(c.TreeDir, "profiles")

	var hub *admin.Hub
	var syncs *atomic.Int64
	if c.Admin != nil {
		var stop func()
		if hub, syncs, stop, err = startAdminEndpoint(c, profiles); err != nil {
			return nil, err
		}
		defer stop()
	}

	out, err := os.Create(filepath.Join(c.TreeDir, "server.out")) //nolint:gosec // inside our tree
	if err != nil {
		return nil, err
	}
	defer func() { _ = out.Close() }()
	cmd, container, err := serverCommand(c, args)
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("boottest: start the server: %w", err)
	}
	logf("server started, pid %d, game %d, query %d, rcon %d", cmd.Process.Pid, ports.Game, ports.Query, ports.RCon)
	// The server is tracked by its process group, never by name: it shows up as enfMain.
	p := &proc{cmd: cmd, done: make(chan struct{}), container: container, podman: podmanBin(c)}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	defer p.stop()

	ev := Eval{Profiles: profiles, Expect: c.Expect, Baseline: c.Baseline, ModScripts: c.ModScripts}
	start := time.Now()
	if exit, ready := waitReady(ctx, ports.Query, c.Timeout, p); ready {
		ev.Ready = true
		// The Steam query answers a couple of seconds after the start, long
		// before the mission has loaded (about 8 s) and the central economy has
		// initialised (about 20 s): wait for the mission, then settle.
		logf("the server answers; waiting for the mission")
		if !waitFor(ctx, c.Timeout-time.Since(start), p, func() bool { return MissionLoaded(profiles) }) && p.alive() {
			ev.NoMission = true
		}
		logf("settling for %s", c.Settle)
		select {
		case <-time.After(c.Settle):
		case <-p.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if hub != nil && p.alive() {
			// the mod's first contact comes after the economy is up
			waitFor(ctx, c.Timeout-time.Since(start), p, func() bool { return hub.Status().Connected })
		}
		if !p.alive() {
			ev.Exited, ev.Ready = "exited after it answered ("+exitText(p.err)+")", false
		}
	} else if exit != "" {
		ev.Exited = exit
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if hub != nil {
		st := hub.Status()
		ok := st.Connected
		ev.Admin, ev.AdminDetail = &ok, fmt.Sprintf("%d sync(s) received, last seen %s", syncs.Load(), st.LastSeen.Format("15:04:05"))
	}
	p.stop()
	return &Result{Checks: Evaluate(ev), Profiles: profiles, Ports: ports}, nil
}

func exitText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

// waitReady polls the Steam query until it answers. The string is non-empty if
// the server exited first.
func waitReady(ctx context.Context, query int, timeout time.Duration, p *proc) (string, bool) {
	deadline := time.After(timeout)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if _, err := a2s.Query("127.0.0.1:"+strconv.Itoa(query), 800*time.Millisecond); err == nil {
			return "", true
		}
		select {
		case <-p.done:
			return "exited (" + exitText(p.err) + ")", false
		case <-deadline:
			return "", false
		case <-ctx.Done():
			return "", false
		case <-tick.C:
		}
	}
}

// waitFor polls cond every half second until it holds, the timeout passes,
// the context ends or the server exits.
func waitFor(ctx context.Context, timeout time.Duration, p *proc, cond func() bool) bool {
	deadline := time.After(timeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		if cond() {
			return true
		}
		select {
		case <-p.done:
			return false
		case <-deadline:
			return false
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

// MissionLoaded reports whether the server has started the mission's scripts:
// its init.c shows up as a script module in the script log (1.29), or the economy has chosen its
// storage directory, which init.c's start triggers (1.30 logs no module for init.c).
func MissionLoaded(profiles string) bool {
	files, _ := filepath.Glob(filepath.Join(profiles, "script_*.log"))
	for _, f := range files {
		for _, l := range lines(f) {
			if strings.Contains(l, "Module: $CurrentDir:mpmissions/") {
				return true
			}
		}
	}
	rpts, _ := filepath.Glob(filepath.Join(profiles, "*.RPT"))
	for _, f := range rpts {
		for _, l := range lines(f) {
			if strings.Contains(l, "[StorageDirs] :: Selected storage directory") {
				return true
			}
		}
	}
	return false
}

// proc is the running server.
type proc struct {
	container, podman string // the container to remove at the end, in container mode
	cmd               *exec.Cmd
	done              chan struct{} // closed when the process has exited
	err               error         // its exit error, valid once done is closed
}

func (p *proc) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// stop ends the server's process group: SIGTERM, then SIGKILL after a grace
// period (after a clean shutdown the process can hang in Steam API threads).
// It is safe to call more than once.
func (p *proc) stop() {
	select {
	case <-p.done:
		return
	default:
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(stopGrace):
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		<-p.done
	}
	if p.container != "" {
		// podman run forwards the signal; make sure that nothing is left behind.
		_ = exec.Command(p.podman, "rm", "-f", "-t", "0", p.container).Run() //nolint:gosec // our own container name
	}
}

func podmanBin(c Config) string {
	if c.Podman != "" {
		return c.Podman
	}
	return "podman"
}

// serverCommand is the command that runs the server: natively, or in the container image. The
// second result is the container's name in container mode.
func serverCommand(c Config, args []string) (*exec.Cmd, string, error) {
	// ulimit -c 0: a crashing server writes up to 5 GB of core otherwise.
	if c.Image == "" {
		cmd := exec.Command("sh", append([]string{"-c", `ulimit -c 0 && exec "$@"`, "sh", "./DayZServer"}, args...)...) //nolint:gosec // our own tree and arguments
		cmd.Dir = c.TreeDir
		return cmd, "", nil
	}
	tree, err := filepath.Abs(c.TreeDir)
	if err != nil {
		return nil, "", err
	}
	srv, err := filepath.Abs(c.ServerDir)
	if err != nil {
		return nil, "", err
	}
	// The experimental builds read the answer to an assertion prompt from stdin (see site.StopConfig).
	if err := os.WriteFile(filepath.Join(tree, "stdin"), []byte(runfiles.StdinAnswer), 0o600); err != nil {
		return nil, "", err
	}
	name := "dzo-boottest-" + runfiles.Hex(4)
	a := []string{"run", "--rm", "--name", name, "--network", "host",
		"-v", tree + ":" + tree, "-v", srv + ":" + srv + ":O", "-w", tree}
	for _, m := range c.Mods {
		dir, err := filepath.Abs(m.Dir)
		if err != nil {
			return nil, "", err
		}
		a = append(a, "-v", dir+":"+dir+":O")
	}
	a = append(a, c.Image, "/bin/sh", "-c", `ulimit -c 0 && exec "$@" <`+shellQuote(filepath.Join(tree, "stdin")), "sh", "./DayZServer")
	a = append(a, args...)
	return exec.Command(podmanBin(c), a...), name, nil //nolint:gosec // the podman binary and our own arguments
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// startAdminEndpoint serves the endpoint the dzo-admin mod posts to, on a
// free local port, and writes the mod's config.json for it.
func startAdminEndpoint(c Config, profiles string) (*admin.Hub, *atomic.Int64, func(), error) {
	tokens, err := os.MkdirTemp("", "dzo-boottest-tokens-")
	if err != nil {
		return nil, nil, nil, err
	}
	reg := admin.NewRegistry(tokens)
	hub := reg.Add(admin.NewHub("boottest"))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = os.RemoveAll(tokens)
		return nil, nil, nil, err
	}
	var syncs atomic.Int64
	handler := reg.ModHandler()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		syncs.Add(1)
		handler.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	cfg := *c.Admin
	cfg.Endpoint = "http://" + ln.Addr().String() + "/"
	if err := admin.WriteModConfig(profiles, tokens, "boottest", cfg); err != nil {
		_ = srv.Close()
		_ = os.RemoveAll(tokens)
		return nil, nil, nil, err
	}
	return hub, &syncs, func() { _ = srv.Close(); _ = os.RemoveAll(tokens) }, nil
}

// BuildTree writes the disposable server tree (§C22): one symlink per entry of
// the server install, real copies of what the test owns.
func BuildTree(c Config, p Ports) error {
	srv, err := filepath.Abs(c.ServerDir)
	if err != nil {
		return err
	}
	tree, err := filepath.Abs(c.TreeDir)
	if err != nil {
		return err
	}
	if strings.HasPrefix(tree+"/", srv+"/") {
		return errors.New("boottest: the tree must not be inside the server install")
	}
	if err := os.MkdirAll(tree, 0o750); err != nil {
		return err
	}
	entries, err := os.ReadDir(srv)
	if err != nil {
		return err
	}
	for _, e := range entries {
		n := e.Name()
		switch {
		case n == "core" || strings.HasPrefix(n, "core.") || strings.HasPrefix(n, "@") || n == "profiles" || n == "serverDZ.cfg" || n == "keys" || n == "mpmissions":
			continue
		case n == "battleye":
			if err := linkContents(filepath.Join(srv, n), filepath.Join(tree, n)); err != nil {
				return err
			}
		default:
			if err := replaceLink(filepath.Join(srv, n), filepath.Join(tree, n)); err != nil {
				return err
			}
		}
	}
	if _, err := os.Stat(filepath.Join(tree, "DayZServer")); err != nil {
		return fmt.Errorf("boottest: %s has no DayZServer: %w", srv, err)
	}

	cfg, err := runfiles.ServerCfg(c.ServerCfg, c.Template, p.Query)
	if err != nil {
		return err
	}
	host := "[dzo boot test]"
	if e, ok := cfg.Get("hostname"); ok && e.Scalar.Value != "" {
		host += " " + e.Scalar.Value
	}
	cfg.SetScalar("hostname", host, true)
	for _, k := range []string{"password", "passwordAdmin"} {
		pw, err := runfiles.RandomPassword(16) // so that nobody can join
		if err != nil {
			return err
		}
		cfg.SetScalar(k, pw, true)
	}
	if err := os.WriteFile(filepath.Join(tree, "serverDZ.cfg"), cfg.Bytes(), 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(tree, "profiles", "battleye"), 0o750); err != nil {
		return err
	}
	pw, err := runfiles.RandomPassword(16)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tree, "profiles", "battleye", "beserver_x64.cfg"), []byte(runfiles.BattlEyeCfg(pw, p.RCon, "127.0.0.1")), 0o600); err != nil {
		return err
	}

	keys := filepath.Join(tree, "keys")
	if err := os.MkdirAll(keys, 0o750); err != nil {
		return err
	}
	for _, k := range c.Keys {
		b, err := os.ReadFile(k) //nolint:gosec // a key found by runfiles.Keys
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(keys, filepath.Base(k)), b, 0o640); err != nil { //nolint:gosec // public keys
			return err
		}
	}
	mission := filepath.Join(tree, "mpmissions", c.Template)
	if err := os.RemoveAll(mission); err != nil {
		return err
	}
	if err := copyTree(c.MissionDir, mission); err != nil {
		return err
	}
	for _, m := range c.Mods {
		if !modName.MatchString(m.Name) {
			return fmt.Errorf("boottest: bad mod name %q", m.Name)
		}
		if _, err := os.Stat(m.Dir); err != nil {
			return fmt.Errorf("boottest: mod %s: %w", m.Name, err)
		}
		if err := replaceLink(m.Dir, filepath.Join(tree, m.Name)); err != nil {
			return err
		}
	}
	return nil
}

func replaceLink(target, link string) error {
	if _, err := os.Lstat(link); err == nil {
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	return os.Symlink(target, link)
}

// linkContents makes dst a real directory holding one symlink per entry of src.
func linkContents(src, dst string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	es, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range es {
		if err := replaceLink(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyTree copies a directory of regular files (a staged mission).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		b, err := os.ReadFile(p) //nolint:gosec // the staged mission
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o640) //nolint:gosec // game data
	})
}
