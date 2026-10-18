// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Sighting is a GUID seen with an IP on an instance. SharedWith counts the other GUIDs seen behind the
// same IP (carrier-grade NAT puts many players behind one address).
type Sighting struct {
	// Ref names the address without giving it away (for a BanInput.IPRefs); IP is empty unless the
	// reader may see address history.
	Ref        string    `json:"ref"`
	GUID       string    `json:"guid"`
	IP         string    `json:"ip,omitempty"`
	Name       string    `json:"name"`
	Instance   string    `json:"instance"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	SharedWith int       `json:"shared_with"`
}

const maxName = 128

// Seen records that a GUID connected from an IP under a name.
func (s *Store) Seen(ctx context.Context, instance, guid, ip, name string, now time.Time) error {
	if !ValidGUID(guid) {
		return invalid("invalid BattlEye GUID %q", guid)
	}
	ip, err := ParseIP(ip)
	if err != nil {
		return err
	}
	if len(name) > maxName {
		name = name[:maxName]
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sightings (guid, ip, name, instance, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (guid, ip, instance) DO UPDATE SET name = excluded.name, last_seen = excluded.last_seen`,
		guid, ip, name, instance, now.Unix(), now.Unix())
	if err != nil {
		return fmt.Errorf("moderation: sighting: %w", err)
	}
	return nil
}

// Identities returns where a Steam ID's GUID was seen, most recent first.
func (s *Store) Identities(ctx context.Context, steamID string) ([]Sighting, error) {
	g, err := DeriveGUID(steamID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.rowid, s.guid, s.ip, s.name, s.instance, s.first_seen, s.last_seen,
			(SELECT COUNT(DISTINCT o.guid) FROM sightings o WHERE o.ip = s.ip AND o.guid <> s.guid)
		FROM sightings s WHERE s.guid = ? ORDER BY s.last_seen DESC`, g)
	if err != nil {
		return nil, fmt.Errorf("moderation: identities: %w", err)
	}
	var out []Sighting
	for rows.Next() {
		var x Sighting
		var first, last, rowid int64
		if err := rows.Scan(&rowid, &x.GUID, &x.IP, &x.Name, &x.Instance, &first, &last, &x.SharedWith); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: identities: %w", err)
		}
		x.Ref = "s" + strconv.FormatInt(rowid, 10)
		x.FirstSeen, x.LastSeen = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
		out = append(out, x)
	}
	return out, closeRows(rows)
}

// CurrentIP is the address a player last connected from on an instance, and when that was seen. It is empty
// for a player never seen there.
func (s *Store) CurrentIP(ctx context.Context, instance, steamID string) (ip string, since time.Time, err error) {
	g, err := DeriveGUID(steamID)
	if err != nil {
		return "", time.Time{}, err
	}
	var last int64
	err = s.db.QueryRowContext(ctx, `SELECT ip, last_seen FROM sightings WHERE guid = ? AND instance = ? ORDER BY last_seen DESC LIMIT 1`, g, instance).Scan(&ip, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("moderation: current IP: %w", err)
	}
	return ip, time.Unix(last, 0).UTC(), nil
}

// resolveIPRefs turns in.IPRefs into in.IPs. A sighting ref must belong to the GUID of the player being banned
// and a target ref to the ban being edited (banID), so a ref cannot name any other address.
func resolveIPRefs(ctx context.Context, q querier, in BanInput, banID int64) (BanInput, error) {
	if len(in.IPRefs) == 0 {
		return in, nil
	}
	var guids []string
	if g, err := DeriveGUID(in.SteamID); err == nil {
		guids = append(guids, g)
	}
	guids = append(guids, in.GUIDs...)
	in.IPs = slices.Clone(in.IPs)
	for _, ref := range in.IPRefs {
		id, err := strconv.ParseInt(ref[min(1, len(ref)):], 10, 64)
		if err != nil || len(ref) < 2 {
			return in, invalid("unknown address reference %q", ref)
		}
		var ip string
		switch ref[0] {
		case 's':
			err = q.QueryRowContext(ctx, `SELECT ip FROM sightings WHERE rowid = ? AND guid IN (`+placeholders(len(guids))+`)`, append([]any{id}, anys(guids)...)...).Scan(&ip)
		case 't':
			err = q.QueryRowContext(ctx, `SELECT value FROM ban_targets WHERE rowid = ? AND ban_id = ? AND kind = 'ip'`, id, banID).Scan(&ip)
		default:
			return in, invalid("unknown address reference %q", ref)
		}
		if errors.Is(err, sql.ErrNoRows) {
			return in, invalid("unknown address reference %q", ref)
		}
		if err != nil {
			return in, fmt.Errorf("moderation: address reference: %w", err)
		}
		if !slices.Contains(in.IPs, ip) {
			in.IPs = append(in.IPs, ip)
		}
	}
	in.IPRefs = nil
	return in, nil
}

func placeholders(n int) string {
	if n == 0 {
		return "NULL"
	}
	return "?" + strings.Repeat(", ?", n-1)
}

func anys(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}
