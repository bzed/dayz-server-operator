// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite" // the database/sql driver "sqlite"
)

//go:embed migrations/sqlite/*.sql
var migrationsFS embed.FS

// Scope says where a ban applies.
type Scope string

const (
	ScopeGlobal    Scope = "global"    // every instance of this installation
	ScopeInstances Scope = "instances" // the instances listed in Ban.Instances
)

// TargetKind is what a ban target identifies.
type TargetKind string

const (
	TargetSteam TargetKind = "steam"
	TargetGUID  TargetKind = "guid"
	TargetIP    TargetKind = "ip"
)

// Target is one identifier a ban covers. Derived marks the GUID computed from the Steam ID.
type Target struct {
	Kind    TargetKind `json:"kind"`
	Value   string     `json:"value"`
	Derived bool       `json:"derived,omitempty"`
}

// Limits of the free-text fields.
const (
	MaxReason      = 1000
	MaxMessage     = 200
	DefaultMessage = "You are banned from this server"
	maxInstanceLen = 64
)

// Ban is one ban with its targets, scope and evidence links.
type Ban struct {
	ID           int64      `json:"id"`
	Reason       string     `json:"reason"`
	Message      string     `json:"message"`
	CreatedBy    string     `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Scope        Scope      `json:"scope"`
	Instances    []string   `json:"instances,omitempty"`
	Targets      []Target   `json:"targets"`
	Evidence     []int64    `json:"evidence,omitempty"`
	RevokedBy    string     `json:"revoked_by,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RevokeReason string     `json:"revoke_reason,omitempty"`
}

// Active reports whether the ban is in force at now.
func (b Ban) Active(now time.Time) bool {
	return b.RevokedAt == nil && (b.ExpiresAt == nil || b.ExpiresAt.After(now))
}

// AppliesTo reports whether the ban's scope covers an instance.
func (b Ban) AppliesTo(instance string) bool {
	return b.Scope == ScopeGlobal || slices.Contains(b.Instances, instance)
}

// State is "active", "expired" or "revoked".
func (b Ban) State(now time.Time) string {
	switch {
	case b.RevokedAt != nil:
		return "revoked"
	case b.Active(now):
		return "active"
	}
	return "expired"
}

// Input returns the editable fields of the ban.
func (b Ban) Input() BanInput {
	in := BanInput{Reason: b.Reason, Message: b.Message, ExpiresAt: b.ExpiresAt, Scope: b.Scope,
		Instances: slices.Clone(b.Instances), Evidence: slices.Clone(b.Evidence)}
	for _, t := range b.Targets {
		switch {
		case t.Kind == TargetSteam:
			in.SteamID = t.Value
		case t.Kind == TargetGUID && !t.Derived:
			in.GUIDs = append(in.GUIDs, t.Value)
		case t.Kind == TargetIP:
			in.IPs = append(in.IPs, t.Value)
		}
	}
	return in
}

// BanInput is what a caller sets when creating or editing a ban.
type BanInput struct {
	SteamID   string     `json:"steam_id,omitempty"`
	GUIDs     []string   `json:"guids,omitempty"`
	IPs       []string   `json:"ips,omitempty"`
	Reason    string     `json:"reason"`
	Message   string     `json:"message,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Scope     Scope      `json:"scope"`
	Instances []string   `json:"instances,omitempty"`
	Evidence  []int64    `json:"evidence,omitempty"`
}

// normalise validates the input and returns it cleaned, with its targets.
func (in BanInput) normalise(now time.Time) (BanInput, []Target, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	in.Message = strings.TrimSpace(in.Message)
	switch {
	case in.Reason == "":
		return in, nil, invalid("a ban needs a reason")
	case utf8.RuneCountInString(in.Reason) > MaxReason:
		return in, nil, invalid("the reason is longer than %d characters", MaxReason)
	case utf8.RuneCountInString(in.Message) > MaxMessage:
		return in, nil, invalid("the message is longer than %d characters", MaxMessage)
	case in.ExpiresAt != nil && !in.ExpiresAt.After(now):
		return in, nil, invalid("the expiry %s is not in the future", FormatExpiry(in.ExpiresAt))
	}
	if in.Message == "" {
		in.Message = DefaultMessage
	}
	if in.ExpiresAt != nil {
		t := in.ExpiresAt.UTC().Truncate(time.Second)
		in.ExpiresAt = &t
	}
	switch in.Scope {
	case ScopeGlobal:
		if len(in.Instances) > 0 {
			return in, nil, invalid("a global ban lists no instances")
		}
	case ScopeInstances:
		if len(in.Instances) == 0 {
			return in, nil, invalid("an instance ban needs at least one instance")
		}
		for _, i := range in.Instances {
			if i == "" || len(i) > maxInstanceLen || strings.ContainsFunc(i, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				return in, nil, invalid("invalid instance name %q", i)
			}
		}
		slices.Sort(in.Instances)
		in.Instances = slices.Compact(in.Instances)
	default:
		return in, nil, invalid("scope must be global or instances, not %q", in.Scope)
	}
	var ts []Target
	add := func(t Target) {
		for _, have := range ts {
			if have.Kind == t.Kind && have.Value == t.Value {
				return
			}
		}
		ts = append(ts, t)
	}
	if in.SteamID != "" {
		g, err := DeriveGUID(in.SteamID)
		if err != nil {
			return in, nil, err
		}
		add(Target{Kind: TargetSteam, Value: in.SteamID})
		add(Target{Kind: TargetGUID, Value: g, Derived: true})
	}
	for i, g := range in.GUIDs {
		g = strings.ToLower(strings.TrimSpace(g))
		if !ValidGUID(g) {
			return in, nil, invalid("invalid BattlEye GUID %q", in.GUIDs[i])
		}
		in.GUIDs[i] = g
		add(Target{Kind: TargetGUID, Value: g})
	}
	for i, ip := range in.IPs {
		v, err := ParseIP(ip)
		if err != nil {
			return in, nil, err
		}
		in.IPs[i] = v
		add(Target{Kind: TargetIP, Value: v})
	}
	if len(ts) == 0 {
		return in, nil, invalid("a ban needs a Steam ID, a GUID or an IP address")
	}
	return in, ts, nil
}

// ErrNotFound is returned for an unknown ban or evidence id.
var ErrNotFound = errors.New("moderation: not found")

// Store is the moderation database.
type Store struct {
	db  *sql.DB
	dir string // evidence files
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func dsn(path string, readOnly bool) string {
	q := "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if readOnly {
		q += "&mode=ro"
	} else {
		q += "&_pragma=journal_mode(WAL)"
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q}).String()
}

// Open opens the database at path, creating it (mode 0600) and applying new migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("moderation: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // dzo's own database path
	if err != nil {
		return nil, fmt.Errorf("moderation: %w", err)
	}
	_ = f.Close() // only created here, so that SQLite does not create it with the umask's mode
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("moderation: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // one writer; transactions queue in the process instead of failing with SQLITE_BUSY
	s := &Store{db: db, dir: filepath.Join(filepath.Dir(path), "evidence")}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// OpenReadOnly opens an existing database without writing to it (the instance start path). A missing
// database gives an error that wraps os.ErrNotExist.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("moderation: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, fmt.Errorf("moderation: open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("moderation: open %s: %w", path, err)
	}
	return &Store{db: db, dir: filepath.Join(filepath.Dir(path), "evidence")}, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("moderation: schema_version: %w", err)
	}
	var cur int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&cur); err != nil {
		return fmt.Errorf("moderation: schema_version: %w", err)
	}
	files, err := fs.Glob(migrationsFS, "migrations/sqlite/*.sql")
	if err != nil {
		return fmt.Errorf("moderation: migrations: %w", err)
	}
	sort.Strings(files)
	for _, f := range files {
		n, err := strconv.Atoi(strings.SplitN(path.Base(f), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("moderation: migration %s: name must start with its number", f)
		}
		if n <= cur {
			continue
		}
		body, err := migrationsFS.ReadFile(f)
		if err != nil {
			return fmt.Errorf("moderation: migration %s: %w", f, err)
		}
		err = s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, n)
			return err
		})
		if err != nil {
			return fmt.Errorf("moderation: migration %s: %w", f, err)
		}
	}
	return nil
}

// tx runs fn in a transaction, committing when it returns nil.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback() // the error of fn is the one that matters
		return err
	}
	return tx.Commit()
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func unixOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func timeOrNil(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}

// CreateBan stores a new ban and its history entry.
func (s *Store) CreateBan(ctx context.Context, actor string, in BanInput, now time.Time) (Ban, error) {
	in, targets, err := in.normalise(now)
	if err != nil {
		return Ban{}, err
	}
	var b Ban
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO bans (reason, message, created_by, created_at, expires_at, scope) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
			in.Reason, in.Message, actor, now.Unix(), unixOrNil(in.ExpiresAt), string(in.Scope)).Scan(&id); err != nil {
			return err
		}
		if err := writeChildren(ctx, tx, id, in, targets); err != nil {
			return err
		}
		var err error
		if b, err = getBan(ctx, tx, id); err != nil {
			return err
		}
		return addHistory(ctx, tx, id, now, actor, "create", map[string]any{"new": b})
	})
	if err != nil {
		return Ban{}, wrap("create ban", err)
	}
	return b, nil
}

// UpdateBan replaces the editable fields of a ban that is not revoked.
func (s *Store) UpdateBan(ctx context.Context, id int64, actor string, in BanInput, now time.Time) (Ban, error) {
	var b Ban
	err := s.tx(ctx, func(tx *sql.Tx) error {
		old, err := getBan(ctx, tx, id)
		if err != nil {
			return err
		}
		if old.RevokedAt != nil {
			return invalid("ban %d is revoked and cannot be edited", id)
		}
		in, targets, err := in.normalise(now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bans SET reason = ?, message = ?, expires_at = ?, scope = ? WHERE id = ?`,
			in.Reason, in.Message, unixOrNil(in.ExpiresAt), string(in.Scope), id); err != nil {
			return err
		}
		if err := writeChildren(ctx, tx, id, in, targets); err != nil {
			return err
		}
		if b, err = getBan(ctx, tx, id); err != nil {
			return err
		}
		return addHistory(ctx, tx, id, now, actor, "edit", map[string]any{"old": old, "new": b})
	})
	if err != nil {
		return Ban{}, wrap("update ban", err)
	}
	return b, nil
}

// RevokeBan ends a ban now. It stays in the database as history.
func (s *Store) RevokeBan(ctx context.Context, id int64, actor, reason string, now time.Time) (Ban, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Ban{}, invalid("revoking a ban needs a reason")
	}
	if utf8.RuneCountInString(reason) > MaxReason {
		return Ban{}, invalid("the reason is longer than %d characters", MaxReason)
	}
	var b Ban
	err := s.tx(ctx, func(tx *sql.Tx) error {
		old, err := getBan(ctx, tx, id)
		if err != nil {
			return err
		}
		if old.RevokedAt != nil {
			return invalid("ban %d is already revoked", id)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bans SET revoked_by = ?, revoked_at = ?, revoke_reason = ? WHERE id = ?`,
			actor, now.Unix(), reason, id); err != nil {
			return err
		}
		if b, err = getBan(ctx, tx, id); err != nil {
			return err
		}
		return addHistory(ctx, tx, id, now, actor, "revoke", map[string]any{"reason": reason})
	})
	if err != nil {
		return Ban{}, wrap("revoke ban", err)
	}
	return b, nil
}

// wrap keeps InvalidError and ErrNotFound recognisable for the API.
func wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	var inv *InvalidError
	if errors.As(err, &inv) || errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("moderation: %s: %w", what, err)
}

func writeChildren(ctx context.Context, tx *sql.Tx, id int64, in BanInput, targets []Target) error {
	for _, q := range []string{`DELETE FROM ban_instances WHERE ban_id = ?`, `DELETE FROM ban_targets WHERE ban_id = ?`, `DELETE FROM ban_evidence WHERE ban_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	for _, i := range in.Instances {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ban_instances (ban_id, instance) VALUES (?, ?)`, id, i); err != nil {
			return err
		}
	}
	for _, t := range targets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ban_targets (ban_id, kind, value, derived) VALUES (?, ?, ?, ?)`, id, string(t.Kind), t.Value, t.Derived); err != nil {
			return err
		}
	}
	for _, e := range in.Evidence {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM evidence WHERE id = ?`, e).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return invalid("unknown evidence id %d", e)
		} else if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ban_evidence (ban_id, evidence_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, e); err != nil {
			return err
		}
	}
	return nil
}

func addHistory(ctx context.Context, tx *sql.Tx, id int64, now time.Time, actor, action string, change any) error {
	b, err := json.Marshal(change)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ban_history (ban_id, at, actor, action, change) VALUES (?, ?, ?, ?, ?)`, id, now.Unix(), actor, action, string(b))
	return err
}

func getBan(ctx context.Context, q querier, id int64) (Ban, error) {
	var b Ban
	var created int64
	var expires, revokedAt sql.NullInt64
	var revokedBy, revokeReason sql.NullString
	var scope string
	err := q.QueryRowContext(ctx, `SELECT id, reason, message, created_by, created_at, expires_at, scope, revoked_by, revoked_at, revoke_reason FROM bans WHERE id = ?`, id).
		Scan(&b.ID, &b.Reason, &b.Message, &b.CreatedBy, &created, &expires, &scope, &revokedBy, &revokedAt, &revokeReason)
	if errors.Is(err, sql.ErrNoRows) {
		return Ban{}, ErrNotFound
	}
	if err != nil {
		return Ban{}, err
	}
	b.CreatedAt, b.ExpiresAt, b.Scope = time.Unix(created, 0).UTC(), timeOrNil(expires), Scope(scope)
	b.RevokedBy, b.RevokedAt, b.RevokeReason = revokedBy.String, timeOrNil(revokedAt), revokeReason.String
	rows, err := q.QueryContext(ctx, `SELECT instance FROM ban_instances WHERE ban_id = ? ORDER BY instance`, id)
	if err != nil {
		return Ban{}, err
	}
	for rows.Next() {
		var i string
		if err := rows.Scan(&i); err != nil {
			_ = rows.Close()
			return Ban{}, err
		}
		b.Instances = append(b.Instances, i)
	}
	if err := closeRows(rows); err != nil {
		return Ban{}, err
	}
	// steam first, then guid, then ip: the order people read them in.
	rows, err = q.QueryContext(ctx, `SELECT kind, value, derived FROM ban_targets WHERE ban_id = ?
		ORDER BY CASE kind WHEN 'steam' THEN 0 WHEN 'guid' THEN 1 ELSE 2 END, derived DESC, value`, id)
	if err != nil {
		return Ban{}, err
	}
	for rows.Next() {
		var t Target
		var kind string
		if err := rows.Scan(&kind, &t.Value, &t.Derived); err != nil {
			_ = rows.Close()
			return Ban{}, err
		}
		t.Kind = TargetKind(kind)
		b.Targets = append(b.Targets, t)
	}
	if err := closeRows(rows); err != nil {
		return Ban{}, err
	}
	rows, err = q.QueryContext(ctx, `SELECT evidence_id FROM ban_evidence WHERE ban_id = ? ORDER BY evidence_id`, id)
	if err != nil {
		return Ban{}, err
	}
	for rows.Next() {
		var e int64
		if err := rows.Scan(&e); err != nil {
			_ = rows.Close()
			return Ban{}, err
		}
		b.Evidence = append(b.Evidence, e)
	}
	return b, closeRows(rows)
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}

// GetBan returns one ban.
func (s *Store) GetBan(ctx context.Context, id int64) (Ban, error) {
	b, err := getBan(ctx, s.db, id)
	return b, wrap("get ban", err)
}

// BanFilter selects bans for ListBans. Empty fields do not filter; Limit 0 means no limit.
type BanFilter struct {
	State    string // "", "active", "expired" or "revoked"
	Instance string // bans that apply to this instance
	Query    string // a Steam ID, GUID or IP among the targets, or text in the reason or a sighted name
	Limit    int
	Now      time.Time
}

// likeEscape makes s a literal for LIKE ... ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListBans returns the bans matching f, newest first.
func (s *Store) ListBans(ctx context.Context, f BanFilter) ([]Ban, error) {
	q := `SELECT b.id FROM bans b WHERE 1 = 1`
	var args []any
	now := f.Now.Unix()
	switch f.State {
	case "":
	case "active":
		q += ` AND b.revoked_at IS NULL AND (b.expires_at IS NULL OR b.expires_at > ?)`
		args = append(args, now)
	case "expired":
		q += ` AND b.revoked_at IS NULL AND b.expires_at IS NOT NULL AND b.expires_at <= ?`
		args = append(args, now)
	case "revoked":
		q += ` AND b.revoked_at IS NOT NULL`
	default:
		return nil, invalid("state must be active, expired or revoked, not %q", f.State)
	}
	if f.Instance != "" {
		q += ` AND (b.scope = 'global' OR EXISTS (SELECT 1 FROM ban_instances i WHERE i.ban_id = b.id AND i.instance = ?))`
		args = append(args, f.Instance)
	}
	if text := strings.TrimSpace(f.Query); text != "" {
		values := []any{text, strings.ToLower(text)}
		if g, err := DeriveGUID(text); err == nil {
			values = append(values, g)
		}
		like := "%" + likeEscape(text) + "%"
		//nolint:gosec // only the number of ? placeholders is concatenated; every value is bound
		q += ` AND (EXISTS (SELECT 1 FROM ban_targets t WHERE t.ban_id = b.id AND t.value IN (?` + strings.Repeat(", ?", len(values)-1) + `))
			OR b.reason LIKE ? ESCAPE '\'
			OR EXISTS (SELECT 1 FROM ban_targets t JOIN sightings g ON g.guid = t.value WHERE t.ban_id = b.id AND g.name LIKE ? ESCAPE '\'))`
		args = append(append(args, values...), like, like)
	}
	q += ` ORDER BY b.id DESC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("moderation: list bans: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: list bans: %w", err)
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("moderation: list bans: %w", err)
	}
	out := make([]Ban, 0, len(ids))
	for _, id := range ids {
		b, err := getBan(ctx, s.db, id)
		if err != nil {
			return nil, fmt.Errorf("moderation: list bans: %w", err)
		}
		out = append(out, b)
	}
	return out, nil
}

// ActiveBans returns every ban in force on an instance at now.
func (s *Store) ActiveBans(ctx context.Context, instance string, now time.Time) ([]Ban, error) {
	return s.ListBans(ctx, BanFilter{State: "active", Instance: instance, Now: now})
}

// FindActive returns the ban in force on an instance for a GUID or an IP (either may be empty),
// preferring a permanent one, then the longest; nil when there is none.
func (s *Store) FindActive(ctx context.Context, instance, guid, ip string, now time.Time) (*Ban, error) {
	if guid == "" && ip == "" {
		return nil, nil
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT b.id FROM bans b JOIN ban_targets t ON t.ban_id = b.id
		WHERE t.kind IN ('guid', 'ip') AND t.value IN (?, ?)
		  AND b.revoked_at IS NULL AND (b.expires_at IS NULL OR b.expires_at > ?)
		  AND (b.scope = 'global' OR EXISTS (SELECT 1 FROM ban_instances i WHERE i.ban_id = b.id AND i.instance = ?))
		ORDER BY b.expires_at IS NULL DESC, b.expires_at DESC, b.id LIMIT 1`, guid, ip, now.Unix(), instance).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("moderation: find ban: %w", err)
	}
	b, err := getBan(ctx, s.db, id)
	if err != nil {
		return nil, fmt.Errorf("moderation: find ban: %w", err)
	}
	return &b, nil
}

// HistoryEntry is one change of a ban.
type HistoryEntry struct {
	At     time.Time       `json:"at"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"` // create, edit or revoke
	Change json.RawMessage `json:"change"`
}

// History returns the changes of a ban, oldest first.
func (s *Store) History(ctx context.Context, id int64) ([]HistoryEntry, error) {
	if _, err := s.GetBan(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT at, actor, action, change FROM ban_history WHERE ban_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("moderation: history: %w", err)
	}
	var out []HistoryEntry
	for rows.Next() {
		var h HistoryEntry
		var at int64
		var change string
		if err := rows.Scan(&at, &h.Actor, &h.Action, &change); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("moderation: history: %w", err)
		}
		h.At, h.Change = time.Unix(at, 0).UTC(), json.RawMessage(change)
		out = append(out, h)
	}
	return out, closeRows(rows)
}
