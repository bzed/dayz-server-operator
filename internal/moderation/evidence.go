// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EvidenceKind is what an evidence item holds.
type EvidenceKind string

const (
	EvidenceLog   EvidenceKind = "log"   // log lines, e.g. from a cheat detector
	EvidenceNote  EvidenceKind = "note"  // an admin's note
	EvidenceImage EvidenceKind = "image" // a screenshot; Body is its caption
)

// Limits of evidence.
const (
	MaxEvidenceBody = 64 << 10
	MaxImage        = 5 << 20
)

// imageTypes are the accepted image types, by what http.DetectContentType says.
var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// Evidence is one item filed against a player.
type Evidence struct {
	ID        int64        `json:"id"`
	SteamID   string       `json:"steam_id,omitempty"`
	GUID      string       `json:"guid,omitempty"`
	IP        string       `json:"ip,omitempty"`
	Instance  string       `json:"instance"`
	Source    string       `json:"source"`
	Kind      EvidenceKind `json:"kind"`
	Body      string       `json:"body"`
	FileType  string       `json:"file_type,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

// EvidenceInput is a new evidence item. Kind is ignored when an image is given.
type EvidenceInput struct {
	SteamID  string       `json:"steam_id,omitempty"`
	GUID     string       `json:"guid,omitempty"`
	IP       string       `json:"ip,omitempty"`
	Instance string       `json:"instance"`
	Kind     EvidenceKind `json:"kind"`
	Body     string       `json:"body"`
}

// Who names a player by any of their identifiers.
type Who struct {
	SteamID, GUID, IP string
}

// resolve validates the identifiers and fills the GUID from the Steam ID.
func (w Who) resolve() (Who, error) {
	if w.SteamID != "" {
		g, err := DeriveGUID(w.SteamID)
		if err != nil {
			return w, err
		}
		if w.GUID != "" && strings.ToLower(w.GUID) != g {
			return w, invalid("GUID %s does not belong to Steam ID %s", w.GUID, w.SteamID)
		}
		w.GUID = g
	}
	if w.GUID != "" {
		w.GUID = strings.ToLower(w.GUID)
		if !ValidGUID(w.GUID) {
			return w, invalid("invalid BattlEye GUID %q", w.GUID)
		}
	}
	if w.IP != "" {
		ip, err := ParseIP(w.IP)
		if err != nil {
			return w, err
		}
		w.IP = ip
	}
	if w.GUID == "" && w.IP == "" {
		return w, invalid("evidence needs a Steam ID, a GUID or an IP address")
	}
	return w, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// AddEvidence stores an evidence item, and the image (if any) as <evidence dir>/<aa>/<sha256>.
func (s *Store) AddEvidence(ctx context.Context, source string, in EvidenceInput, image []byte, now time.Time) (Evidence, error) {
	who, err := Who{SteamID: in.SteamID, GUID: in.GUID, IP: in.IP}.resolve()
	if err != nil {
		return Evidence{}, err
	}
	in.Instance = strings.TrimSpace(in.Instance)
	if in.Instance == "" || len(in.Instance) > maxInstanceLen {
		return Evidence{}, invalid("evidence needs the instance it is from")
	}
	in.Body = strings.TrimSpace(strings.ReplaceAll(in.Body, "\x00", ""))
	if len(in.Body) > MaxEvidenceBody {
		return Evidence{}, invalid("the evidence text is longer than %d bytes", MaxEvidenceBody)
	}
	var sum, ctype string
	if image != nil {
		if len(image) == 0 || len(image) > MaxImage {
			return Evidence{}, invalid("an image must be 1 byte to %d MiB", MaxImage>>20)
		}
		ctype = http.DetectContentType(image)
		if !imageTypes[ctype] {
			return Evidence{}, invalid("images must be PNG, JPEG or WebP, not %s", ctype)
		}
		h := sha256.Sum256(image)
		sum = hex.EncodeToString(h[:])
		if err := s.writeFile(sum, image); err != nil {
			return Evidence{}, err
		}
		in.Kind = EvidenceImage
	} else {
		if in.Kind != EvidenceLog && in.Kind != EvidenceNote {
			return Evidence{}, invalid("evidence kind must be log or note (images are uploaded as files), not %q", in.Kind)
		}
		if in.Body == "" {
			return Evidence{}, invalid("evidence needs some text")
		}
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `INSERT INTO evidence (steam_id, guid, ip, instance, source, kind, body, file_sha256, file_type, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		nullable(who.SteamID), nullable(who.GUID), nullable(who.IP), in.Instance, source, string(in.Kind), in.Body, nullable(sum), nullable(ctype), now.Unix()).Scan(&id)
	if err != nil {
		return Evidence{}, fmt.Errorf("moderation: add evidence: %w", err)
	}
	return s.GetEvidence(ctx, id)
}

func (s *Store) filePath(sum string) string { return filepath.Join(s.dir, sum[:2], sum) }

// writeFile stores data under its hash, atomically, unless it is there already.
func (s *Store) writeFile(sum string, data []byte) error {
	p := s.filePath(sum)
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return fmt.Errorf("moderation: evidence file: %w", err)
	}
	return writeAtomic(p, data)
}

// writeAtomic writes data to path through a temporary file in the same directory (mode 0600).
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("moderation: write %s: %w", filepath.Base(path), err)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort; the write error is what matters
		return fmt.Errorf("moderation: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

const evidenceCols = `id, COALESCE(steam_id, ''), COALESCE(guid, ''), COALESCE(ip, ''), instance, source, kind, body, COALESCE(file_type, ''), created_at`

func scanEvidence(sc interface{ Scan(...any) error }) (Evidence, error) {
	var e Evidence
	var kind string
	var created int64
	if err := sc.Scan(&e.ID, &e.SteamID, &e.GUID, &e.IP, &e.Instance, &e.Source, &kind, &e.Body, &e.FileType, &created); err != nil {
		return Evidence{}, err
	}
	e.Kind, e.CreatedAt = EvidenceKind(kind), time.Unix(created, 0).UTC()
	return e, nil
}

// GetEvidence returns one evidence item.
func (s *Store) GetEvidence(ctx context.Context, id int64) (Evidence, error) {
	e, err := scanEvidence(s.db.QueryRowContext(ctx, `SELECT `+evidenceCols+` FROM evidence WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Evidence{}, ErrNotFound
	}
	if err != nil {
		return Evidence{}, fmt.Errorf("moderation: evidence: %w", err)
	}
	return e, nil
}

// EvidenceFor returns the evidence filed against a player (by GUID, which a Steam ID resolves to,
// or by IP), newest first, at most 500 items.
func (s *Store) EvidenceFor(ctx context.Context, who Who) ([]Evidence, error) {
	who, err := who.resolve()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+evidenceCols+` FROM evidence WHERE guid = ? OR ip = ? ORDER BY id DESC LIMIT 500`, who.GUID, who.IP)
	if err != nil {
		return nil, fmt.Errorf("moderation: evidence: %w", err)
	}
	var out []Evidence
	for rows.Next() {
		e, err := scanEvidence(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: evidence: %w", err)
		}
		out = append(out, e)
	}
	return out, closeRows(rows)
}

// EvidenceFile returns the stored file of an image evidence item and its content type.
func (s *Store) EvidenceFile(ctx context.Context, id int64) (path, contentType string, err error) {
	var sum, ctype sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT file_sha256, file_type FROM evidence WHERE id = ?`, id).Scan(&sum, &ctype)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !sum.Valid) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("moderation: evidence file: %w", err)
	}
	return s.filePath(sum.String), ctype.String, nil
}
