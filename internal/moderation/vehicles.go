// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const maxVehicleID = 64

// VehicleSeen is a vehicle of a snapshot, with its last drive when the mod reported one.
type VehicleSeen struct {
	ID, Type, Driver, DriverName string
	DrivenAt                     time.Time // zero: no drive reported
}

// VehicleRecord is what dzo remembers of a vehicle.
type VehicleRecord struct {
	ID, Type, Driver, DriverName  string
	DrivenAt, FirstSeen, LastSeen time.Time // DrivenAt zero: never seen driven
}

// LastUse is when the vehicle was last driven, or first seen when it never was.
func (r VehicleRecord) LastUse() time.Time {
	if r.DrivenAt.IsZero() {
		return r.FirstSeen
	}
	return r.DrivenAt
}

// newerDrive is the SQL condition under which a row takes the reported driver: the type changed (a reused
// id starts over) or the reported drive is newer than the stored one.
const newerDrive = `(vehicles.type <> excluded.type
	OR (excluded.driven_at IS NOT NULL AND (vehicles.driven_at IS NULL OR excluded.driven_at > vehicles.driven_at)))`

// SyncVehicles records a snapshot: seen vehicles are created or updated, and vehicles of the instance missing
// from it are hidden, not deleted. A recorded drive is only replaced by a newer one, and a vehicle whose type
// changed starts over. Vehicles with an id longer than 64 bytes are skipped, and a driver with an invalid
// Steam ID is not recorded.
func (s *Store) SyncVehicles(ctx context.Context, instance string, seen []VehicleSeen, now time.Time) error {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		// Hide every visible row first; the upserts below make the seen ones visible again.
		if _, err := tx.ExecContext(ctx, `UPDATE vehicles SET hidden_since = ? WHERE instance = ? AND hidden_since IS NULL`, now.Unix(), instance); err != nil {
			return err
		}
		for _, v := range seen {
			if v.ID == "" || len(v.ID) > maxVehicleID {
				continue
			}
			var drivenAt any
			driver, name := v.Driver, clip(v.DriverName, maxName)
			if ValidSteamID(driver) && !v.DrivenAt.IsZero() {
				drivenAt = v.DrivenAt.Unix()
			} else {
				driver, name = "", ""
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO vehicles (instance, vehicle_id, type, driver_steam_id, driver_name, driven_at, first_seen, last_seen, hidden_since)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
				ON CONFLICT (instance, vehicle_id) DO UPDATE SET
					driver_steam_id = CASE WHEN `+newerDrive+` THEN excluded.driver_steam_id ELSE vehicles.driver_steam_id END,
					driver_name = CASE WHEN `+newerDrive+` THEN excluded.driver_name ELSE vehicles.driver_name END,
					driven_at = CASE WHEN `+newerDrive+` THEN excluded.driven_at ELSE vehicles.driven_at END,
					first_seen = CASE WHEN vehicles.type <> excluded.type THEN excluded.first_seen ELSE vehicles.first_seen END,
					type = excluded.type,
					last_seen = excluded.last_seen,
					hidden_since = NULL`,
				instance, v.ID, clip(v.Type, maxName), driver, name, drivenAt, now.Unix(), now.Unix()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("moderation: sync vehicles: %w", err)
	}
	return nil
}

// VehicleRecords returns the visible (not hidden) records of an instance by vehicle id.
func (s *Store) VehicleRecords(ctx context.Context, instance string) (map[string]VehicleRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT vehicle_id, type, driver_steam_id, driver_name, driven_at, first_seen, last_seen
		FROM vehicles WHERE instance = ? AND hidden_since IS NULL`, instance)
	if err != nil {
		return nil, fmt.Errorf("moderation: vehicles: %w", err)
	}
	out := map[string]VehicleRecord{}
	for rows.Next() {
		var r VehicleRecord
		var driven sql.NullInt64
		var first, last int64
		if err := rows.Scan(&r.ID, &r.Type, &r.Driver, &r.DriverName, &driven, &first, &last); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: vehicles: %w", err)
		}
		if driven.Valid {
			r.DrivenAt = time.Unix(driven.Int64, 0).UTC()
		}
		r.FirstSeen, r.LastSeen = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
		out[r.ID] = r
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	return out, nil
}

// ForgetVehicles deletes the rows of an instance hidden before cutoff and returns how many.
func (s *Store) ForgetVehicles(ctx context.Context, instance string, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM vehicles WHERE instance = ? AND hidden_since IS NOT NULL AND hidden_since < ?`, instance, cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("moderation: forget vehicles: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("moderation: forget vehicles: %w", err)
	}
	return n, nil
}
