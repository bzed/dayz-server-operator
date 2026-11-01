// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package web

import (
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/admin"
	"github.com/bzed/dayz-server-operator/internal/api"
)

func TestE2ECleanupFlow(t *testing.T) {
	e := cleanupFixture(t, api.RoleAdmin, admin.FeatureDriver)
	useRealAssets(t, e.assets)
	defer e.answerCommands(true, "deleted")()
	b := newBrowser(t)
	b.run(t, chromedp.Navigate(e.url("/b/main/i/alpha")), chromedp.WaitVisible(`button[hx-get$="/vehicles/cleanup"]`))
	b.clickUntil(t, `button[hx-get$="/vehicles/cleanup"]`, func() bool { return b.count(`#cleanup button[name=days][value="30"]`) == 1 })
	b.clickUntil(t, `#cleanup button[name=days][value="30"]`, func() bool { return b.count(`#cleanup input[name=id]`) == 2 })
	require.Contains(t, b.textOf("#cleanup"), "Delete 2 vehicles")

	b.dialogs <- true
	b.clickUntil(t, `#cleanup form[hx-post] button`, func() bool { return len(b.dialogs) == 0 })
	require.Eventually(t, func() bool { return e.commandsOf("vehicle_delete") == 2 }, 10*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool { return b.count(`#cleanup input[name=id]`) == 0 && b.count(`#cleanup .flash`) > 0 }, 10*time.Second, 50*time.Millisecond)
	require.Contains(t, b.textOf("#cleanup"), "Deleted 2 vehicles")
}
