// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/log"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// The browser tests run against the JavaScript a dzo installation really serves: htmx, Leaflet and Fork Awesome
// from the Debian packages libjs-htmx, libjs-leaflet and fonts-fork-awesome, not the stubs of newEnv.
const (
	e2eChromeEnv = "DZO_E2E_CHROME"
	e2eAssetsEnv = "DZO_E2E_ASSETS"
)

// useRealAssets copies htmx and Leaflet from $DZO_E2E_ASSETS (default /usr/share/javascript) into dst, the
// assets directory of a web server.
func useRealAssets(t *testing.T, dst string) {
	t.Helper()
	src := os.Getenv(e2eAssetsEnv)
	if src == "" {
		src = "/usr/share/javascript"
	}
	files := []string{"htmx/htmx.min.js", "leaflet/leaflet.js", "leaflet/leaflet.css", "fork-awesome/css/fork-awesome.css"}
	for _, glob := range []string{"leaflet/images/*", "fork-awesome/fonts/*"} {
		matches, err := filepath.Glob(filepath.Join(src, glob))
		require.NoError(t, err)
		for _, m := range matches {
			rel, err := filepath.Rel(src, m)
			require.NoError(t, err)
			files = append(files, rel)
		}
	}
	for _, f := range files {
		from := filepath.Join(src, f)
		require.FileExists(t, from, "install libjs-htmx, libjs-leaflet and fonts-fork-awesome or set %s", e2eAssetsEnv)
		in, err := os.Open(from) //nolint:gosec // a path of the test environment
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(dst, filepath.Dir(f)), 0o750))
		out, err := os.Create(filepath.Join(dst, f)) //nolint:gosec // under the test's temp directory
		require.NoError(t, err)
		_, err = io.Copy(out, in)
		require.NoError(t, err)
		require.NoError(t, in.Close())
		require.NoError(t, out.Close())
	}
}

// browser is one headless Chrome with its own profile directory. Everything that goes wrong in the page,
// a JavaScript exception, a console error or a CSP report, fails the test when it ends.
type browser struct {
	ctx     context.Context
	dialogs chan<- bool // the answer for the next window.confirm: true accepts
	// expect makes a problem that contains text an expected one, for a test that breaks a connection on purpose.
	expect func(text string)
}

func newBrowser(t *testing.T) *browser {
	t.Helper()
	return newBrowserWithin(t, 30*time.Second)
}

// newBrowserWithin is newBrowser with another budget for the whole test: the live tier waits for a real
// server and for htmx polling.
func newBrowserWithin(t *testing.T, budget time.Duration) *browser {
	t.Helper()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	profile := filepath.Join(t.TempDir(), "profile")
	opts = append(opts, chromedp.UserDataDir(profile))
	// Chrome's helper processes outlive the main one; their own process group lets the cleanup stop them all.
	opts = append(opts, chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		cmd.SysProcAttr.Setpgid = true
	}))
	if p := os.Getenv(e2eChromeEnv); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	timeout, cancelTimeout := context.WithTimeout(t.Context(), budget)
	alloc, cancelAlloc := chromedp.NewExecAllocator(timeout, opts...)
	ctx, _ := chromedp.NewContext(alloc, chromedp.WithErrorf(t.Logf)) //nolint:govet // stopped by chromedp.Cancel below, which also waits for the browser
	// Cleanups run last in, first out: the problem report below runs before the browser is stopped.
	var pgid int
	t.Cleanup(func() {
		// Cancel waits for the main process; the helpers are stopped with their process group.
		_ = chromedp.Cancel(ctx)
		if pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		cancelAlloc()
		cancelTimeout()
		// Chrome's helper processes can still hold files for a moment; t.TempDir's own removal does not retry.
		require.Eventually(t, func() bool { return os.RemoveAll(profile) == nil }, 10*time.Second, 50*time.Millisecond)
	})

	var mu sync.Mutex
	var problems, expected []string
	report := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		msg := fmt.Sprintf(format, args...)
		for _, e := range expected {
			if strings.Contains(msg, e) {
				return
			}
		}
		problems = append(problems, msg)
	}
	answers := make(chan bool, 8)
	chromedp.ListenTarget(ctx, func(ev any) {
		switch ev := ev.(type) {
		case *runtime.EventExceptionThrown:
			report("exception: %s", ev.ExceptionDetails.Error())
		case *runtime.EventConsoleAPICalled:
			if ev.Type != runtime.APITypeError {
				return
			}
			var parts []string
			for _, a := range ev.Args {
				parts = append(parts, strings.Trim(string(a.Value), `"`))
			}
			report("console.error: %s", strings.Join(parts, " "))
		case *log.EventEntryAdded:
			if ev.Entry.Level == log.LevelError {
				report("%s: %s %s", ev.Entry.Source, ev.Entry.Text, ev.Entry.URL)
			}
		case *cdppage.EventJavascriptDialogOpening:
			accept := false
			select {
			case accept = <-answers:
			default:
				report("unexpected dialog: %s", ev.Message)
			}
			// The listener must not block the browser's event loop: answer from a goroutine.
			go func() {
				_ = chromedp.Run(ctx, cdppage.HandleJavaScriptDialog(accept))
			}()
		}
	})
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		require.Empty(t, problems, "the browser reported problems:\n%s", strings.Join(problems, "\n"))
	})

	if err := chromedp.Run(ctx, log.Enable(), runtime.Enable()); err != nil {
		require.NoError(t, err, "Chrome/Chromium did not start (is one installed, with its sandbox? set %s for another binary)", e2eChromeEnv)
	}
	if c := chromedp.FromContext(ctx); c != nil && c.Browser != nil && c.Browser.Process() != nil {
		pgid = c.Browser.Process().Pid
	}
	return &browser{ctx: ctx, dialogs: answers, expect: func(text string) {
		mu.Lock()
		defer mu.Unlock()
		expected = append(expected, text)
	}}
}

func (b *browser) run(t *testing.T, actions ...chromedp.Action) {
	t.Helper()
	require.NoError(t, chromedp.Run(b.ctx, actions...))
}

func (e *env) url(path string) string { return e.web.URL + path }

// clickUntil clicks sel until done reports the click's effect. htmx binds the elements it has just swapped in
// a moment after they are visible, and a click before that does nothing; the clicks it is used for are
// idempotent (opening a panel, a dialog, answering a dismissed confirmation).
func (b *browser) clickUntil(t *testing.T, sel string, done func() bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		if done() {
			return true
		}
		_ = chromedp.Run(b.ctx, chromedp.Click(sel, chromedp.ByQuery))
		return false
	}, 15*time.Second, 300*time.Millisecond, "clicking %s had no effect", sel)
}

// drag presses the left button on the centre of the first element that matches sel, moves through path (pixel
// offsets from the start, in order) in steps interpolated moves per segment and releases. The 16 ms between the
// moves paces the input like a hand; nothing waits for a result with it.
func (b *browser) drag(t *testing.T, sel string, path [][2]float64, steps int) {
	t.Helper()
	b.press(t, sel)
	b.moveBy(t, path, steps)
	b.run(t, chromedp.Sleep(100*time.Millisecond)) // the hand rests a moment: the icon catches up with the pointer
	b.release(t)
}

// dragPos is where the pointer is, in viewport coordinates, between press and release.
var dragPos [2]float64

func (b *browser) press(t *testing.T, sel string) {
	t.Helper()
	var r []float64
	b.run(t, chromedp.Evaluate(`(function(){var r=document.querySelector(`+jsString(sel)+`).getBoundingClientRect();return [r.left+r.width/2,r.top+r.height/2];})()`, &r))
	require.Len(t, r, 2)
	dragPos = [2]float64{r[0], r[1]}
	b.run(t,
		input.DispatchMouseEvent(input.MouseMoved, r[0], r[1]),
		input.DispatchMouseEvent(input.MousePressed, r[0], r[1]).WithButton(input.Left).WithClickCount(1),
	)
}

func (b *browser) moveBy(t *testing.T, path [][2]float64, steps int) {
	t.Helper()
	for _, d := range path {
		from := dragPos
		for i := 1; i <= steps; i++ {
			f := float64(i) / float64(steps)
			x, y := from[0]+d[0]*f, from[1]+d[1]*f
			b.run(t, input.DispatchMouseEvent(input.MouseMoved, x, y).WithButton(input.Left).WithButtons(1), chromedp.Sleep(16*time.Millisecond))
		}
		dragPos = [2]float64{from[0] + d[0], from[1] + d[1]}
	}
}

func (b *browser) release(t *testing.T) {
	t.Helper()
	b.run(t, input.DispatchMouseEvent(input.MouseReleased, dragPos[0], dragPos[1]).WithButton(input.Left).WithClickCount(1))
}

// mapZoom sets the zoom of the map without animation.
func (b *browser) mapZoom(t *testing.T, z float64) {
	t.Helper()
	b.run(t, chromedp.Evaluate(fmt.Sprintf(`document.getElementById('map')._dzoMap.setZoom(%v,{animate:false});void 0`, z), nil))
}
