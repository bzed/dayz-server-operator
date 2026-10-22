// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"fmt"

	"github.com/bzed/dayz-server-operator/internal/battleye"
	"github.com/bzed/dayz-server-operator/internal/config"
	"github.com/bzed/dayz-server-operator/internal/lookups"
	"github.com/bzed/dayz-server-operator/internal/steamweb"
)

// newLookups builds the refresher of the player pages' BattlEye and Steam answers. BattlEye's master is
// always asked (it is information, not enforcement); Steam is asked unless steam.profile_lookup is off, with
// the Web API key of steam.web_api_key_file when there is one. A key file that cannot be used is an error
// that stops dzo serve: running without the key the operator configured would be a surprise.
func newLookups(cfg *config.Config, version string, store lookups.Store, logf func(string, ...any)) (*lookups.Refresher, error) {
	ref := &lookups.Refresher{Store: store, BE: &battleye.Master{}, SteamOn: cfg.Steam.Lookup(), Logf: logf}
	if !ref.SteamOn {
		return ref, nil
	}
	client := &steamweb.Client{UserAgent: "dzo/" + version, Wait: ref.SteamWait}
	if path := cfg.Steam.WebAPIKeyFile; path != "" {
		key, err := steamweb.ReadKey(path)
		if err != nil {
			return nil, fmt.Errorf("steam.web_api_key_file: %w", err)
		}
		client.Key, ref.SteamKey = key, true
	}
	ref.Steam = client
	return ref, nil
}
