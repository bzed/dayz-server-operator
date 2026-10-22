// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package steamweb reads what Steam says about a player: the community profile page (no key) and, with a Web
// API key, the ban counts, account details, DayZ playtime, level and friends. The answers are information for
// the player page; nothing here changes anything at Steam.
//
// The key is a secret: it travels in the query string, so no error of this package carries a URL.
package steamweb

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bzed/dayz-server-operator/internal/moderation"
)

const (
	// DayZAppID is DayZ's Steam app id.
	DayZAppID = 221100
	// MaxFriends caps the friend list kept per player.
	MaxFriends = 2000
	// MaxBans is how many ids the ban and summary endpoints take at once.
	MaxBans = 100

	defaultCommunity = "https://steamcommunity.com"
	defaultAPI       = "https://api.steampowered.com"
	maxBody          = 256 << 10
	timeout          = 10 * time.Second
)

// ProfileURL is the link to a player's Steam profile; empty for an invalid Steam ID.
func ProfileURL(steamID string) string {
	if !moderation.ValidSteamID(steamID) {
		return ""
	}
	return defaultCommunity + "/profiles/" + steamID
}

var keyRE = regexp.MustCompile(`^[0-9A-Fa-f]{32}$`)

// ReadKey reads a Steam Web API key from a file. The file must not be readable by group or others; the error
// names the path, never the content.
func ReadKey(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("steam web api key: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("steam web api key: %s is readable by others (mode %04o); chmod 600 it", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path) //nolint:gosec // the configured key file
	if err != nil {
		return "", fmt.Errorf("steam web api key: %w", err)
	}
	k := strings.TrimSpace(string(b))
	if !keyRE.MatchString(k) {
		return "", fmt.Errorf("steam web api key: %s does not hold a key (32 hex digits)", path)
	}
	return k, nil
}

// Client asks Steam. The zero value with a Key works against the real hosts.
type Client struct {
	Key           string // Web API key; empty: only Community works
	CommunityBase string // default https://steamcommunity.com
	APIBase       string // default https://api.steampowered.com
	HTTP          *http.Client
	UserAgent     string // sent with every request, "dzo/<version>"
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: timeout}
}

// get fetches one endpoint and returns its body, at most maxBody bytes. endpoint names it in errors; the URL
// never appears there because it holds the key.
func (c *Client) get(ctx context.Context, endpoint, rawURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("steam: %s: request", endpoint)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // net/http's wrapper holds the URL
		}
		return nil, 0, fmt.Errorf("steam: %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("steam: %s: reading the answer: %w", endpoint, err)
	}
	if len(body) > maxBody {
		return nil, resp.StatusCode, fmt.Errorf("steam: %s: the answer is larger than %d KiB", endpoint, maxBody>>10)
	}
	return body, resp.StatusCode, nil
}

func (c *Client) api(ctx context.Context, endpoint, path string, q url.Values) ([]byte, int, error) {
	base := c.APIBase
	if base == "" {
		base = defaultAPI
	}
	q.Set("key", c.Key)
	return c.get(ctx, endpoint, base+path+"?"+q.Encode())
}

type communityProfile struct {
	Privacy string `xml:"privacyState"`
	Name    string `xml:"steamID"`
	VAC     string `xml:"vacBanned"`
	Trade   string `xml:"tradeBanState"`
	Limited string `xml:"isLimitedAccount"`
	Since   string `xml:"memberSince"`
	Place   string `xml:"location"`
	Games   []struct {
		Link    string `xml:"gameLink"`
		OnTotal string `xml:"hoursOnRecord"`
	} `xml:"mostPlayedGames>mostPlayedGame"`
	Err string `xml:"error"`
}

// Community reads the public profile page (https://steamcommunity.com/profiles/<id>?xml=1). It is not an
// official API: a profile that cannot be read gives the state "unknown", not an error.
func (c *Client) Community(ctx context.Context, steamID string) (moderation.SteamInfo, error) {
	if !moderation.ValidSteamID(steamID) {
		return moderation.SteamInfo{}, fmt.Errorf("steam: %q is not a Steam ID", steamID)
	}
	base := c.CommunityBase
	if base == "" {
		base = defaultCommunity
	}
	body, status, err := c.get(ctx, "community profile", base+"/profiles/"+steamID+"?xml=1")
	if err != nil {
		return moderation.SteamInfo{}, err
	}
	if status != http.StatusOK {
		return moderation.SteamInfo{}, fmt.Errorf("steam: community profile: HTTP %d", status)
	}
	var p communityProfile
	if err := xml.Unmarshal(body, &p); err != nil {
		return moderation.SteamInfo{}, fmt.Errorf("steam: community profile: unreadable answer: %w", err)
	}
	if p.Err != "" || p.Privacy == "" {
		return moderation.SteamInfo{State: moderation.LookupUnknown}, nil
	}
	in := moderation.SteamInfo{State: moderation.LookupOK, Source: "community", Persona: strings.TrimSpace(p.Name), Privacy: p.Privacy,
		Location: strings.TrimSpace(p.Place), MemberSince: strings.TrimSpace(p.Since), TradeBan: strings.TrimSpace(p.Trade),
		Limited: p.Limited == "1", VACBanned: p.VAC == "1"}
	for _, g := range p.Games {
		if strings.HasSuffix(strings.TrimSpace(g.Link), "/app/"+strconv.Itoa(DayZAppID)) {
			if h, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(g.OnTotal), ",", ""), 64); err == nil {
				in.DayZHours = &h
			}
		}
	}
	return in, nil
}

func (c *Client) needKey() error {
	if c.Key == "" {
		return errors.New("steam: no Web API key configured")
	}
	return nil
}

// Bans asks the Web API for the bans and account details of up to 100 players (key only). Players Steam does
// not know are left out of the result.
func (c *Client) Bans(ctx context.Context, ids []string) (map[string]moderation.SteamInfo, error) {
	if err := c.needKey(); err != nil {
		return nil, err
	}
	if len(ids) == 0 || len(ids) > MaxBans {
		return nil, fmt.Errorf("steam: Bans takes 1 to %d ids, not %d", MaxBans, len(ids))
	}
	for _, id := range ids {
		if !moderation.ValidSteamID(id) {
			return nil, fmt.Errorf("steam: %q is not a Steam ID", id)
		}
	}
	q := url.Values{"steamids": {strings.Join(ids, ",")}}
	body, status, err := c.api(ctx, "GetPlayerBans", "/ISteamUser/GetPlayerBans/v1/", q)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("steam: GetPlayerBans: HTTP %d", status)
	}
	var bans struct {
		Players []struct {
			SteamID          string `json:"SteamId"`
			CommunityBanned  bool
			VACBanned        bool
			NumberOfVACBans  int
			DaysSinceLastBan int
			NumberOfGameBans int
			EconomyBan       string
		} `json:"players"`
	}
	if err := json.Unmarshal(body, &bans); err != nil {
		return nil, fmt.Errorf("steam: GetPlayerBans: unreadable answer")
	}
	out := map[string]moderation.SteamInfo{}
	for _, b := range bans.Players {
		if !moderation.ValidSteamID(b.SteamID) {
			continue
		}
		vac, game, days := b.NumberOfVACBans, b.NumberOfGameBans, b.DaysSinceLastBan
		out[b.SteamID] = moderation.SteamInfo{State: moderation.LookupOK, Source: "api", CommunityBanned: b.CommunityBanned, VACBanned: b.VACBanned,
			VACBans: &vac, GameBans: &game, DaysSinceBan: &days, EconomyBan: b.EconomyBan}
	}
	// The summaries add the name, privacy, account age and country. They are a bonus: the bans above are the
	// point, so a failure here keeps them.
	if sbody, sstatus, err := c.api(ctx, "GetPlayerSummaries", "/ISteamUser/GetPlayerSummaries/v0002/", q); err == nil && sstatus == http.StatusOK {
		var sum struct {
			Response struct {
				Players []struct {
					SteamID    string `json:"steamid"`
					Visibility int    `json:"communityvisibilitystate"`
					Name       string `json:"personaname"`
					Created    int64  `json:"timecreated"`
					Country    string `json:"loccountrycode"`
				} `json:"players"`
			} `json:"response"`
		}
		if json.Unmarshal(sbody, &sum) == nil {
			for _, s := range sum.Response.Players {
				in, ok := out[s.SteamID]
				if !ok {
					continue
				}
				in.Persona, in.Country = s.Name, s.Country
				in.Privacy = "private"
				if s.Visibility == 3 {
					in.Privacy = "public"
				}
				if s.Created > 0 {
					t := time.Unix(s.Created, 0).UTC()
					in.Created = &t
				}
				out[s.SteamID] = in
			}
		}
	}
	return out, nil
}

// Details adds what needs one request per player (key only): DayZ playtime, the Steam level and the public
// friends. A part the player keeps private (HTTP 401 or 403, or an empty answer) is left empty, not an error.
func (c *Client) Details(ctx context.Context, steamID string, in *moderation.SteamInfo) error {
	if err := c.needKey(); err != nil {
		return err
	}
	if !moderation.ValidSteamID(steamID) {
		return fmt.Errorf("steam: %q is not a Steam ID", steamID)
	}
	if body, ok, err := c.private(ctx, "GetOwnedGames", "/IPlayerService/GetOwnedGames/v1/", url.Values{
		"steamid": {steamID}, "appids_filter[0]": {strconv.Itoa(DayZAppID)}, "include_played_free_games": {"1"}}); err != nil {
		return err
	} else if ok {
		var r struct {
			Response struct {
				Games []struct {
					AppID    int `json:"appid"`
					Playtime int `json:"playtime_forever"`
				} `json:"games"`
			} `json:"response"`
		}
		if json.Unmarshal(body, &r) == nil {
			for _, g := range r.Response.Games {
				if g.AppID == DayZAppID {
					h := float64(g.Playtime) / 60
					in.DayZHours = &h
				}
			}
		}
	}
	if body, ok, err := c.private(ctx, "GetSteamLevel", "/IPlayerService/GetSteamLevel/v1/", url.Values{"steamid": {steamID}}); err != nil {
		return err
	} else if ok {
		var r struct {
			Response struct {
				Level *int `json:"player_level"`
			} `json:"response"`
		}
		if json.Unmarshal(body, &r) == nil {
			in.Level = r.Response.Level
		}
	}
	if body, ok, err := c.private(ctx, "GetFriendList", "/ISteamUser/GetFriendList/v0001/", url.Values{"steamid": {steamID}, "relationship": {"friend"}}); err != nil {
		return err
	} else if ok {
		var r struct {
			List struct {
				Friends []struct {
					SteamID string `json:"steamid"`
				} `json:"friends"`
			} `json:"friendslist"`
		}
		if json.Unmarshal(body, &r) == nil {
			in.FriendIDs = nil
			for _, f := range r.List.Friends {
				if moderation.ValidSteamID(f.SteamID) && len(in.FriendIDs) < MaxFriends {
					in.FriendIDs = append(in.FriendIDs, f.SteamID)
				}
			}
		}
	}
	return nil
}

// private fetches an endpoint whose data a player may keep private: ok is false for 401 and 403.
func (c *Client) private(ctx context.Context, endpoint, path string, q url.Values) (body []byte, ok bool, err error) {
	body, status, err := c.api(ctx, endpoint, path, q)
	switch {
	case err != nil:
		return nil, false, err
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, false, nil
	case status != http.StatusOK:
		return nil, false, fmt.Errorf("steam: %s: HTTP %d", endpoint, status)
	}
	return body, true, nil
}
