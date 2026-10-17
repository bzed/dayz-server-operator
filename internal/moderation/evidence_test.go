// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// png1x1 is a minimal valid PNG header followed by filler; http.DetectContentType only needs the header.
var png1x1 = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{0}, 64)...)

func TestAddEvidenceLogDerivesTheGUID(t *testing.T) {
	s, _ := newStore(t)
	e, err := s.AddEvidence(ctx, "detector:speed", EvidenceInput{SteamID: steamA, Instance: "alpha", Kind: EvidenceLog, Body: "speed 900 km/h\x00"}, nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if e.GUID != guidOf(t, steamA) || e.Body != "speed 900 km/h" || e.Source != "detector:speed" {
		t.Fatalf("evidence %+v", e)
	}
	list, err := s.EvidenceFor(ctx, Who{GUID: guidOf(t, steamA)})
	if err != nil || len(list) != 1 || list[0].ID != e.ID {
		t.Fatalf("EvidenceFor = %+v, %v", list, err)
	}
	b, err := s.CreateBan(ctx, "a", BanInput{SteamID: steamA, Reason: "speedhack", Scope: ScopeGlobal, Evidence: []int64{e.ID}}, t0)
	if err != nil || len(b.Evidence) != 1 || b.Evidence[0] != e.ID {
		t.Fatalf("ban evidence %+v, %v", b.Evidence, err)
	}
}

func TestAddEvidenceValidates(t *testing.T) {
	s, _ := newStore(t)
	for name, in := range map[string]EvidenceInput{
		"nobody":        {Instance: "alpha", Kind: EvidenceNote, Body: "x"},
		"no instance":   {SteamID: steamA, Kind: EvidenceNote, Body: "x"},
		"empty body":    {SteamID: steamA, Instance: "alpha", Kind: EvidenceNote, Body: "  "},
		"bad kind":      {SteamID: steamA, Instance: "alpha", Kind: "video", Body: "x"},
		"image kind":    {SteamID: steamA, Instance: "alpha", Kind: EvidenceImage, Body: "x"},
		"guid mismatch": {SteamID: steamA, GUID: guidOf(t, steamB), Instance: "alpha", Kind: EvidenceNote, Body: "x"},
		"too long":      {SteamID: steamA, Instance: "alpha", Kind: EvidenceLog, Body: strings.Repeat("l", MaxEvidenceBody+1)},
	} {
		var inv *InvalidError
		if _, err := s.AddEvidence(ctx, "a", in, nil, t0); !errors.As(err, &inv) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAddEvidenceImageIsStoredByHash(t *testing.T) {
	s, _ := newStore(t)
	e, err := s.AddEvidence(ctx, "alice", EvidenceInput{IP: "192.0.2.7", Instance: "alpha", Body: "screenshot"}, png1x1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != EvidenceImage || e.FileType != "image/png" {
		t.Fatalf("evidence %+v", e)
	}
	path, ct, err := s.EvidenceFile(ctx, e.ID)
	if err != nil || ct != "image/png" {
		t.Fatalf("EvidenceFile = %q, %q, %v", path, ct, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, %v", info.Mode().Perm(), err)
	}
	e2, err := s.AddEvidence(ctx, "alice", EvidenceInput{IP: "192.0.2.7", Instance: "alpha"}, png1x1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if p2, _, _ := s.EvidenceFile(ctx, e2.ID); p2 != path {
		t.Fatalf("the same image is stored twice: %s, %s", path, p2)
	}
	if _, _, err := s.EvidenceFile(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing evidence file: %v", err)
	}
}

func TestAddEvidenceRefusesNonImages(t *testing.T) {
	s, _ := newStore(t)
	for name, data := range map[string][]byte{
		"html":    []byte("<!DOCTYPE html><script>alert(1)</script>"),
		"svg":     []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"too big": append(png1x1, bytes.Repeat([]byte{0}, MaxImage)...),
		"empty":   {},
		"gif":     []byte("GIF89a......"),
	} {
		var inv *InvalidError
		if _, err := s.AddEvidence(ctx, "a", EvidenceInput{SteamID: steamA, Instance: "alpha"}, data, t0); !errors.As(err, &inv) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
