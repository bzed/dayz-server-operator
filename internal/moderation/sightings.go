// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"fmt"
	"time"
)

// Sighting is a GUID seen with an IP on an instance. SharedWith counts the other GUIDs seen behind the
// same IP (carrier-grade NAT puts many players behind one address).
type Sighting struct {
	GUID       string    `json:"guid"`
	IP         string    `json:"ip"`
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
	rows, err := s.db.QueryContext(ctx, `SELECT s.guid, s.ip, s.name, s.instance, s.first_seen, s.last_seen,
			(SELECT COUNT(DISTINCT o.guid) FROM sightings o WHERE o.ip = s.ip AND o.guid <> s.guid)
		FROM sightings s WHERE s.guid = ? ORDER BY s.last_seen DESC`, g)
	if err != nil {
		return nil, fmt.Errorf("moderation: identities: %w", err)
	}
	var out []Sighting
	for rows.Next() {
		var x Sighting
		var first, last int64
		if err := rows.Scan(&x.GUID, &x.IP, &x.Name, &x.Instance, &first, &last, &x.SharedWith); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: identities: %w", err)
		}
		x.FirstSeen, x.LastSeen = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
		out = append(out, x)
	}
	return out, closeRows(rows)
}
