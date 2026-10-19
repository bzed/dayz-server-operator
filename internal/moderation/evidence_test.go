// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package moderation

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// png1x1 is a minimal valid PNG header followed by filler; http.DetectContentType only needs the header.
var png1x1 = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{0}, 64)...)

func TestAddEvidenceLogDerivesTheGUID(t *testing.T) {
	s, _ := newStore(t)
	e, err := s.AddEvidence(t.Context(), "detector:speed", EvidenceInput{SteamID: steamA, Instance: "alpha", Kind: EvidenceLog, Body: "speed 900 km/h\x00"}, nil, t0)
	require.NoError(t, err)
	require.Equal(t, guidOf(t, steamA), e.GUID)
	require.Equal(t, "speed 900 km/h", e.Body)
	require.Equal(t, "detector:speed", e.Source)
	list, err := s.EvidenceFor(t.Context(), Who{GUID: guidOf(t, steamA)})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, e.ID, list[0].ID)
	b, err := s.CreateBan(t.Context(), "a", BanInput{SteamID: steamA, Reason: "speedhack", Scope: ScopeGlobal, Evidence: []int64{e.ID}}, t0)
	require.NoError(t, err)
	require.Len(t, b.Evidence, 1)
	require.Equal(t, e.ID, b.Evidence[0])
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
		_, err := s.AddEvidence(t.Context(), "a", in, nil, t0)
		require.ErrorAs(t, err, &inv, "%s", name)
	}
}

func TestAddEvidenceImageIsStoredByHash(t *testing.T) {
	s, _ := newStore(t)
	e, err := s.AddEvidence(t.Context(), "alice", EvidenceInput{IP: "192.0.2.7", Instance: "alpha", Body: "screenshot"}, png1x1, t0)
	require.NoError(t, err)
	require.Equal(t, EvidenceImage, e.Kind)
	require.Equal(t, "image/png", e.FileType)
	path, ct, err := s.EvidenceFile(t.Context(), e.ID)
	require.NoError(t, err)
	require.Equal(t, "image/png", ct)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	e2, err := s.AddEvidence(t.Context(), "alice", EvidenceInput{IP: "192.0.2.7", Instance: "alpha"}, png1x1, t0)
	require.NoError(t, err)
	p2, _, _ := s.EvidenceFile(t.Context(), e2.ID)
	require.Equal(t, path, p2)
	_, _, err = s.EvidenceFile(t.Context(), 999)
	require.ErrorIs(t, err, ErrNotFound)
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
		_, err := s.AddEvidence(t.Context(), "a", EvidenceInput{SteamID: steamA, Instance: "alpha"}, data, t0)
		require.ErrorAs(t, err, &inv, "%s", name)
	}
}
