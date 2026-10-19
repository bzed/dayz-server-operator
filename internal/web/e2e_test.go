// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
}

func newBrowser(t *testing.T) *browser {
	t.Helper()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	profile := filepath.Join(t.TempDir(), "profile")
	opts = append(opts, chromedp.UserDataDir(profile))
	if p := os.Getenv(e2eChromeEnv); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	timeout, cancelTimeout := context.WithTimeout(t.Context(), 30*time.Second)
	alloc, cancelAlloc := chromedp.NewExecAllocator(timeout, opts...)
	ctx, _ := chromedp.NewContext(alloc) //nolint:govet // stopped by chromedp.Cancel below, which also waits for the browser
	// Cleanups run last in, first out: the problem report below runs before the browser is stopped.
	t.Cleanup(func() {
		// Cancel waits for Chrome to exit; it writes to the profile directory until then.
		_ = chromedp.Cancel(ctx)
		cancelAlloc()
		cancelTimeout()
		// Chrome's helper processes can still hold files for a moment; t.TempDir's own removal does not retry.
		require.Eventually(t, func() bool { return os.RemoveAll(profile) == nil }, 10*time.Second, 50*time.Millisecond)
	})

	var mu sync.Mutex
	var problems []string
	report := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		problems = append(problems, fmt.Sprintf(format, args...))
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
		require.NoError(t, err, "no Chrome/Chromium found; install one or set %s", e2eChromeEnv)
	}
	return &browser{ctx: ctx, dialogs: answers}
}

func (b *browser) run(t *testing.T, actions ...chromedp.Action) {
	t.Helper()
	require.NoError(t, chromedp.Run(b.ctx, actions...))
}

func (e *env) url(path string) string { return e.web.URL + path }
