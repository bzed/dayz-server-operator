// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrLookupTooSoon is returned when a player was looked up on request a moment ago.
var ErrLookupTooSoon = errors.New("moderation: this player was looked up a moment ago")

// PlayerName is a name a player used.
type PlayerName struct {
	Name      string    `json:"name"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// PlayerInstance is what a player did on one instance. Played counts closed sessions only.
type PlayerInstance struct {
	Instance  string        `json:"instance"`
	FirstSeen time.Time     `json:"first_seen"`
	LastSeen  time.Time     `json:"last_seen"`
	Sessions  int           `json:"sessions"`
	Played    time.Duration `json:"played"`
}

// Session is one stay on an instance; End is nil while the player is still there.
type Session struct {
	Instance string     `json:"instance"`
	Name     string     `json:"name"`
	Start    time.Time  `json:"start"`
	End      *time.Time `json:"end,omitempty"`
}

// LookupState says what an asked source answered.
type LookupState string

// The states of a lookup: LookupOK is a usable answer, LookupUnknown that the source was asked and gave none
// (it never replaces a known answer), and the empty state that it was never asked.
const (
	LookupOK      LookupState = "ok"
	LookupUnknown LookupState = "unknown"
)

// BEGame is a game BattlEye's master server answers for.
type BEGame string

// The BattlEye games, in the order the page shows them.
const (
	BEDayZ    BEGame = "dayz"
	BEArma3   BEGame = "arma3"
	BEArma2OA BEGame = "arma2oa"
)

// BEGames lists the games in display order.
var BEGames = []BEGame{BEDayZ, BEArma3, BEArma2OA}

// BEStatus is BattlEye's global ban status of a GUID in one game.
type BEStatus struct {
	Game      BEGame      `json:"game"`
	State     LookupState `json:"state"`
	Banned    bool        `json:"banned"`
	Reason    string      `json:"reason,omitempty"`
	CheckedAt time.Time   `json:"checked_at"`
}

// SteamInfo is what Steam said about a Steam ID. Without a Web API key only the community page is read, which
// leaves the counts, creation time, country, level and friends empty.
type SteamInfo struct {
	State       LookupState `json:"state"`
	CheckedAt   time.Time   `json:"checked_at"`
	Source      string      `json:"source,omitempty"` // "community" or "api"
	Persona     string      `json:"persona,omitempty"`
	Privacy     string      `json:"privacy,omitempty"`
	Location    string      `json:"location,omitempty"`
	MemberSince string      `json:"member_since,omitempty"`
	TradeBan    string      `json:"trade_ban,omitempty"`

	Limited         bool     `json:"limited,omitempty"`
	VACBanned       bool     `json:"vac_banned,omitempty"`
	CommunityBanned bool     `json:"community_banned,omitempty"`
	VACBans         *int     `json:"vac_bans,omitempty"`
	GameBans        *int     `json:"game_bans,omitempty"`
	DaysSinceBan    *int     `json:"days_since_last_ban,omitempty"`
	EconomyBan      string   `json:"economy_ban,omitempty"`
	DayZHours       *float64 `json:"dayz_hours,omitempty"`

	Created *time.Time `json:"created,omitempty"`
	Country string     `json:"country,omitempty"`
	Level   *int       `json:"level,omitempty"`
	// FriendIDs are the public Steam friends (key only); SaveSteamInfo replaces the player's rows.
	FriendIDs []string `json:"friend_ids,omitempty"`
}

// Banned reports whether Steam flags the account: a VAC, game or community ban.
func (i SteamInfo) Banned() bool {
	return i.VACBanned || i.CommunityBanned || (i.GameBans != nil && *i.GameBans > 0)
}

// PlayerSummary is a player in a list.
type PlayerSummary struct {
	SteamID   string    `json:"steam_id"`
	GUID      string    `json:"guid"`
	Name      string    `json:"name"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Instances []string  `json:"instances"`
	Online    bool      `json:"online"`
	// BE has one entry per game that was asked, in the order dayz, arma3, arma2oa.
	BE          []BEStatus `json:"be,omitempty"`
	BEBanned    bool       `json:"be_banned"`
	SteamBanned bool       `json:"steam_banned"`
	// HasBan: an active ban of ours covers the player (set by Players, which knows the time).
	HasBan bool `json:"has_ban"`
}

// PlayerRecord is everything the database knows about one player.
type PlayerRecord struct {
	PlayerSummary
	Names       []PlayerName     `json:"names"`
	PerInstance []PlayerInstance `json:"per_instance"`
	Sessions    []Session        `json:"sessions"`
	Steam       SteamInfo        `json:"steam"`
}

// PlayerFilter selects players for Players. Limit defaults to 50 and is at most 200.
type PlayerFilter struct {
	Query    string // a Steam ID, a GUID or a part of a name the player ever used
	Instance string
	// Only restricts the players to those seen on one of these instances (a token for some instances); empty
	// does not restrict.
	Only    []string
	Flagged bool // only players with a BattlEye ban, a Steam ban or an active ban of ours
	Limit   int
	Offset  int
	Now     time.Time // when "active" is judged; zero is now
}

const (
	defaultPlayerLimit = 50
	maxPlayerLimit     = 200
	maxReason          = 256
	recentSessions     = 50
	friendChunk        = 500
)

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// PlayerJoined records that a player is on an instance: the player, the name and a new session. An open
// session of the same player and instance is closed first.
func (s *Store) PlayerJoined(ctx context.Context, instance, steamID, name string, at time.Time) error {
	guid, err := DeriveGUID(steamID)
	if err != nil {
		return err
	}
	name = clip(name, maxName)
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO players (steam_id, guid, name, first_seen, last_seen) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (steam_id) DO UPDATE SET name = excluded.name, last_seen = MAX(last_seen, excluded.last_seen), first_seen = MIN(first_seen, excluded.first_seen)`,
			steamID, guid, name, at.Unix(), at.Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_names (steam_id, name, first_seen, last_seen) VALUES (?, ?, ?, ?)
			ON CONFLICT (steam_id, name) DO UPDATE SET last_seen = MAX(last_seen, excluded.last_seen)`,
			steamID, name, at.Unix(), at.Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE player_sessions SET ended_at = MAX(started_at, ?) WHERE steam_id = ? AND instance = ? AND ended_at IS NULL`,
			at.Unix(), steamID, instance); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO player_sessions (steam_id, instance, name, started_at) VALUES (?, ?, ?, ?)`, steamID, instance, name, at.Unix())
		return err
	})
	if err != nil {
		return fmt.Errorf("moderation: player joined: %w", err)
	}
	return nil
}

// PlayerLeft closes the player's open session on an instance.
func (s *Store) PlayerLeft(ctx context.Context, instance, steamID string, at time.Time) error {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE player_sessions SET ended_at = MAX(started_at, ?) WHERE steam_id = ? AND instance = ? AND ended_at IS NULL`,
			at.Unix(), steamID, instance); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE players SET last_seen = MAX(last_seen, ?) WHERE steam_id = ?`, at.Unix(), steamID)
		return err
	})
	if err != nil {
		return fmt.Errorf("moderation: player left: %w", err)
	}
	return nil
}

// CloseSessions closes every open session of an instance, as when the server stopped or the mod went away.
func (s *Store) CloseSessions(ctx context.Context, instance string, at time.Time) error {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE players SET last_seen = MAX(last_seen, ?) WHERE steam_id IN
			(SELECT steam_id FROM player_sessions WHERE instance = ? AND ended_at IS NULL)`, at.Unix(), instance); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE player_sessions SET ended_at = MAX(started_at, ?) WHERE instance = ? AND ended_at IS NULL`, at.Unix(), instance)
		return err
	})
	if err != nil {
		return fmt.Errorf("moderation: close sessions: %w", err)
	}
	return nil
}

// PurgeSessions deletes the sessions that ended before cutoff. The players, their names and first-seen times stay.
func (s *Store) PurgeSessions(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM player_sessions WHERE ended_at IS NOT NULL AND ended_at < ?`, cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("moderation: purge sessions: %w", err)
	}
	return res.RowsAffected()
}

const activeBan = `EXISTS (SELECT 1 FROM ban_targets t JOIN bans b ON b.id = t.ban_id
	WHERE ((t.kind = 'steam' AND t.value = p.steam_id) OR (t.kind = 'guid' AND t.value = p.guid))
	AND b.revoked_at IS NULL AND (b.expires_at IS NULL OR b.expires_at > ?))`

// Players returns the players matching f, most recently seen first, and how many match in all.
func (s *Store) Players(ctx context.Context, f PlayerFilter) ([]PlayerSummary, int, error) {
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	where, args := ` WHERE 1 = 1`, []any{}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + likeEscape(q) + "%"
		where += ` AND (p.steam_id = ? OR p.guid = ? OR p.name LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM player_names n WHERE n.steam_id = p.steam_id AND n.name LIKE ? ESCAPE '\'))`
		args = append(args, q, strings.ToLower(q), like, like)
	}
	if f.Instance != "" {
		where += ` AND EXISTS (SELECT 1 FROM player_sessions x WHERE x.steam_id = p.steam_id AND x.instance = ?)`
		args = append(args, f.Instance)
	}
	if len(f.Only) > 0 {
		where += ` AND EXISTS (SELECT 1 FROM player_sessions o WHERE o.steam_id = p.steam_id AND o.instance IN (` + strings.TrimSuffix(strings.Repeat("?,", len(f.Only)), ",") + `))`
		for _, i := range f.Only {
			args = append(args, i)
		}
	}
	if f.Flagged {
		where += ` AND (EXISTS (SELECT 1 FROM be_status b WHERE b.guid = p.guid AND b.banned = 1)
			OR EXISTS (SELECT 1 FROM steam_info i WHERE i.steam_id = p.steam_id AND i.banned = 1) OR ` + activeBan + `)`
		args = append(args, now.Unix())
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM players p`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("moderation: players: %w", err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultPlayerLimit
	}
	limit = min(limit, maxPlayerLimit)
	//nolint:gosec // the clauses are constants of this file; every value is bound
	rows, err := s.db.QueryContext(ctx, `SELECT p.steam_id, p.guid, p.name, p.first_seen, p.last_seen, `+activeBan+` FROM players p`+where+
		` ORDER BY p.last_seen DESC, p.steam_id LIMIT ? OFFSET ?`, append(append([]any{now.Unix()}, args...), limit, max(f.Offset, 0))...)
	if err != nil {
		return nil, 0, fmt.Errorf("moderation: players: %w", err)
	}
	var out []PlayerSummary
	for rows.Next() {
		var p PlayerSummary
		var first, last int64
		if err := rows.Scan(&p.SteamID, &p.GUID, &p.Name, &first, &last, &p.HasBan); err != nil {
			_ = rows.Close()
			return nil, 0, fmt.Errorf("moderation: players: %w", err)
		}
		p.FirstSeen, p.LastSeen = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, fmt.Errorf("moderation: players: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("moderation: players: %w", err)
	}
	for i := range out {
		if err := s.fillSummary(ctx, &out[i]); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

// fillSummary adds the instances, the online state and the verdicts of the lookups.
func (s *Store) fillSummary(ctx context.Context, p *PlayerSummary) error {
	rows, err := s.db.QueryContext(ctx, `SELECT instance, MAX(ended_at IS NULL) FROM player_sessions WHERE steam_id = ? GROUP BY instance ORDER BY MAX(started_at) DESC`, p.SteamID)
	if err != nil {
		return fmt.Errorf("moderation: player instances: %w", err)
	}
	for rows.Next() {
		var inst string
		var open bool
		if err := rows.Scan(&inst, &open); err != nil {
			_ = rows.Close()
			return fmt.Errorf("moderation: player instances: %w", err)
		}
		p.Instances = append(p.Instances, inst)
		p.Online = p.Online || open
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("moderation: player instances: %w", err)
	}
	be, err := s.beStatus(ctx, p.GUID)
	if err != nil {
		return err
	}
	p.BE = be
	for _, b := range be {
		p.BEBanned = p.BEBanned || b.Banned
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT banned FROM steam_info WHERE steam_id = ?), 0)`, p.SteamID).Scan(&p.SteamBanned); err != nil {
		return fmt.Errorf("moderation: steam ban flag: %w", err)
	}
	return nil
}

func (s *Store) beStatus(ctx context.Context, guid string) ([]BEStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT game, state, banned, reason, checked_at FROM be_status WHERE guid = ?`, guid)
	if err != nil {
		return nil, fmt.Errorf("moderation: battleye status: %w", err)
	}
	byGame := map[BEGame]BEStatus{}
	for rows.Next() {
		var b BEStatus
		var state string
		var at int64
		if err := rows.Scan(&b.Game, &state, &b.Banned, &b.Reason, &at); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: battleye status: %w", err)
		}
		b.State, b.CheckedAt = LookupState(state), time.Unix(at, 0).UTC()
		byGame[b.Game] = b
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("moderation: battleye status: %w", err)
	}
	var out []BEStatus
	for _, g := range BEGames {
		if b, ok := byGame[g]; ok {
			out = append(out, b)
		}
	}
	return out, nil
}

// Player returns everything known about one player; ErrNotFound when the Steam ID never connected.
func (s *Store) Player(ctx context.Context, steamID string) (PlayerRecord, error) {
	var r PlayerRecord
	var first, last int64
	err := s.db.QueryRowContext(ctx, `SELECT steam_id, guid, name, first_seen, last_seen FROM players WHERE steam_id = ?`, steamID).
		Scan(&r.SteamID, &r.GUID, &r.Name, &first, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, fmt.Errorf("moderation: player: %w", err)
	}
	r.FirstSeen, r.LastSeen = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
	if err := s.fillSummary(ctx, &r.PlayerSummary); err != nil {
		return r, err
	}
	if r.Names, err = s.playerNames(ctx, steamID); err != nil {
		return r, err
	}
	if r.PerInstance, err = s.playerInstances(ctx, steamID); err != nil {
		return r, err
	}
	if r.Sessions, err = s.playerSessions(ctx, steamID); err != nil {
		return r, err
	}
	if r.Steam, err = s.steamInfo(ctx, steamID); err != nil {
		return r, err
	}
	return r, nil
}

func (s *Store) playerNames(ctx context.Context, steamID string) ([]PlayerName, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, first_seen, last_seen FROM player_names WHERE steam_id = ? ORDER BY last_seen DESC, first_seen DESC`, steamID)
	if err != nil {
		return nil, fmt.Errorf("moderation: player names: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PlayerName
	for rows.Next() {
		var n PlayerName
		var f, l int64
		if err := rows.Scan(&n.Name, &f, &l); err != nil {
			return nil, fmt.Errorf("moderation: player names: %w", err)
		}
		n.FirstSeen, n.LastSeen = time.Unix(f, 0).UTC(), time.Unix(l, 0).UTC()
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) playerInstances(ctx context.Context, steamID string) ([]PlayerInstance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance, MIN(started_at), MAX(COALESCE(ended_at, started_at)), COUNT(*),
			COALESCE(SUM(CASE WHEN ended_at IS NULL THEN 0 ELSE ended_at - started_at END), 0)
		FROM player_sessions WHERE steam_id = ? GROUP BY instance ORDER BY MAX(started_at) DESC`, steamID)
	if err != nil {
		return nil, fmt.Errorf("moderation: player instances: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PlayerInstance
	for rows.Next() {
		var x PlayerInstance
		var f, l, played int64
		if err := rows.Scan(&x.Instance, &f, &l, &x.Sessions, &played); err != nil {
			return nil, fmt.Errorf("moderation: player instances: %w", err)
		}
		x.FirstSeen, x.LastSeen, x.Played = time.Unix(f, 0).UTC(), time.Unix(l, 0).UTC(), time.Duration(played)*time.Second
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) playerSessions(ctx context.Context, steamID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance, name, started_at, ended_at FROM player_sessions WHERE steam_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`, steamID, recentSessions)
	if err != nil {
		return nil, fmt.Errorf("moderation: player sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Session
	for rows.Next() {
		var x Session
		var st int64
		var end sql.NullInt64
		if err := rows.Scan(&x.Instance, &x.Name, &st, &end); err != nil {
			return nil, fmt.Errorf("moderation: player sessions: %w", err)
		}
		x.Start, x.End = time.Unix(st, 0).UTC(), timeOrNil(end)
		out = append(out, x)
	}
	return out, rows.Err()
}

// SaveBEStatus stores BattlEye's answer for a GUID in one game. An unknown answer only records that and when
// the master was asked: a known answer is kept.
func (s *Store) SaveBEStatus(ctx context.Context, guid string, st BEStatus) error {
	if !ValidGUID(guid) {
		return invalid("invalid BattlEye GUID %q", guid)
	}
	var err error
	if st.State == LookupUnknown {
		_, err = s.db.ExecContext(ctx, `INSERT INTO be_status (guid, game, state, banned, reason, checked_at) VALUES (?, ?, ?, 0, '', ?)
			ON CONFLICT (guid, game) DO UPDATE SET state = excluded.state, checked_at = excluded.checked_at`,
			guid, string(st.Game), string(st.State), st.CheckedAt.Unix())
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO be_status (guid, game, state, banned, reason, checked_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (guid, game) DO UPDATE SET state = excluded.state, banned = excluded.banned, reason = excluded.reason, checked_at = excluded.checked_at`,
			guid, string(st.Game), string(st.State), st.Banned, clip(st.Reason, maxReason), st.CheckedAt.Unix())
	}
	if err != nil {
		return fmt.Errorf("moderation: save battleye status: %w", err)
	}
	return nil
}

func (s *Store) steamInfo(ctx context.Context, steamID string) (SteamInfo, error) {
	var in SteamInfo
	var state, data string
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT state, data, checked_at FROM steam_info WHERE steam_id = ?`, steamID).Scan(&state, &data, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return in, nil
	}
	if err != nil {
		return in, fmt.Errorf("moderation: steam info: %w", err)
	}
	if err := json.Unmarshal([]byte(data), &in); err != nil {
		return in, fmt.Errorf("moderation: steam info: %w", err)
	}
	in.State, in.CheckedAt = LookupState(state), time.Unix(at, 0).UTC()
	rows, err := s.db.QueryContext(ctx, `SELECT friend_id FROM steam_friends WHERE steam_id = ? ORDER BY friend_id`, steamID)
	if err != nil {
		return in, fmt.Errorf("moderation: steam friends: %w", err)
	}
	defer func() { _ = rows.Close() }()
	in.FriendIDs = nil
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return in, fmt.Errorf("moderation: steam friends: %w", err)
		}
		in.FriendIDs = append(in.FriendIDs, id)
	}
	return in, rows.Err()
}

// SaveSteamInfo stores what Steam said about a player and replaces the player's friend list. An unknown answer
// only records that and when Steam was asked: a known answer and its friends are kept.
func (s *Store) SaveSteamInfo(ctx context.Context, steamID string, in SteamInfo) error {
	if !ValidSteamID(steamID) {
		return invalid("invalid Steam ID %q", steamID)
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if in.State == LookupUnknown {
			res, err := tx.ExecContext(ctx, `UPDATE steam_info SET state = ?, checked_at = ? WHERE steam_id = ?`, string(in.State), in.CheckedAt.Unix(), steamID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 { //nolint:errcheck // sqlite reports it; 0 means there is no known answer to keep
				return nil
			}
		}
		in.Persona, in.Location = clip(in.Persona, maxName), clip(in.Location, maxName)
		friends := in.FriendIDs
		in.FriendIDs = nil
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO steam_info (steam_id, state, banned, data, checked_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (steam_id) DO UPDATE SET state = excluded.state, banned = excluded.banned, data = excluded.data, checked_at = excluded.checked_at`,
			steamID, string(in.State), in.Banned(), string(data), in.CheckedAt.Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM steam_friends WHERE steam_id = ?`, steamID); err != nil {
			return err
		}
		for _, f := range friends {
			if !ValidSteamID(f) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO steam_friends (steam_id, friend_id) VALUES (?, ?)`, steamID, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("moderation: save steam info: %w", err)
	}
	return nil
}

// FriendsSeen returns the players among friendIDs that connected to us, most recently seen first.
func (s *Store) FriendsSeen(ctx context.Context, friendIDs []string) ([]PlayerSummary, error) {
	var out []PlayerSummary
	for len(friendIDs) > 0 {
		chunk := friendIDs[:min(friendChunk, len(friendIDs))]
		friendIDs = friendIDs[len(chunk):]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		//nolint:gosec // only the number of ? placeholders is concatenated; every value is bound
		rows, err := s.db.QueryContext(ctx, `SELECT steam_id, guid, name, first_seen, last_seen FROM players WHERE steam_id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("moderation: friends seen: %w", err)
		}
		for rows.Next() {
			var p PlayerSummary
			var f, l int64
			if err := rows.Scan(&p.SteamID, &p.GUID, &p.Name, &f, &l); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("moderation: friends seen: %w", err)
			}
			p.FirstSeen, p.LastSeen = time.Unix(f, 0).UTC(), time.Unix(l, 0).UTC()
			out = append(out, p)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("moderation: friends seen: %w", err)
		}
	}
	for i := range out {
		if err := s.fillSummary(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(out, func(a, b PlayerSummary) int { return b.LastSeen.Compare(a.LastSeen) })
	return out, nil
}

// DueLookups returns what is due for a lookup, never-asked first and then the longest ago: kind "be" gives
// GUIDs with a game not asked since checkedBefore, kind "steam" Steam IDs. Only players seen since seenAfter.
func (s *Store) DueLookups(ctx context.Context, kind string, checkedBefore, seenAfter time.Time, limit int) ([]string, error) {
	var q string
	var args []any
	switch kind {
	case "be":
		q = `SELECT p.guid FROM players p WHERE p.last_seen >= ?
			AND (SELECT COUNT(*) FROM be_status b WHERE b.guid = p.guid AND b.checked_at >= ?) < ?
			ORDER BY CASE WHEN (SELECT COUNT(*) FROM be_status b WHERE b.guid = p.guid) < ? THEN 0
				ELSE (SELECT MIN(b.checked_at) FROM be_status b WHERE b.guid = p.guid) END, p.last_seen DESC LIMIT ?`
		args = []any{seenAfter.Unix(), checkedBefore.Unix(), len(BEGames), len(BEGames), limit}
	case "steam":
		q = `SELECT p.steam_id FROM players p LEFT JOIN steam_info i ON i.steam_id = p.steam_id
			WHERE p.last_seen >= ? AND (i.steam_id IS NULL OR i.checked_at < ?)
			ORDER BY COALESCE(i.checked_at, 0), p.last_seen DESC LIMIT ?`
		args = []any{seenAfter.Unix(), checkedBefore.Unix(), limit}
	default:
		return nil, fmt.Errorf("moderation: unknown lookup kind %q", kind)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("moderation: due lookups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("moderation: due lookups: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
