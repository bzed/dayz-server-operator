// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderDefaultTemplate(t *testing.T) {
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	msg, err := ts.Render(Event{
		Kind:     KindHealthUnhealthy,
		Instance: "deerisle",
	})
	require.NoError(t, err, "Render")
	require.Contains(t, msg, "deerisle")
	assert.Contains(t, msg, "health check failing")
}

func TestRenderWithData(t *testing.T) {
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	msg, err := ts.Render(Event{
		Kind:     KindModUpdateDetected,
		Instance: "hashima",
		Data:     map[string]any{"Count": 3},
	})
	require.NoError(t, err, "Render")
	require.Contains(t, msg, "hashima")
	assert.Contains(t, msg, "3 mod update")
}

func TestRenderUnknownKindFallsBack(t *testing.T) {
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	msg, err := ts.Render(Event{Kind: Kind("custom_event"), Instance: "x", Data: map[string]any{"foo": "bar"}})
	require.NoError(t, err, "Render")
	require.Contains(t, msg, "custom_event")
	assert.Contains(t, msg, "x")
}

func TestOverrideTemplate(t *testing.T) {
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	err = ts.Override(KindHealthUnhealthy, "CUSTOM {{.Instance}}")
	require.NoError(t, err, "Override")
	msg, err := ts.Render(Event{Kind: KindHealthUnhealthy, Instance: "y"})
	require.NoError(t, err, "Render")
	assert.Equal(t, "CUSTOM y", msg)
}

func TestOverrideInvalidTemplate(t *testing.T) {
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	err = ts.Override(KindHealthUnhealthy, "{{ .Broken ")
	require.Error(t, err, "expected an error for invalid template syntax")
}

func TestRenderCoalescedSingleEventUsesNormalTemplate(t *testing.T) {
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	msg, err := ts.RenderCoalesced(KindHealthUnhealthy, []Event{{Kind: KindHealthUnhealthy, Instance: "z"}})
	require.NoError(t, err, "RenderCoalesced")
	assert.Contains(t, msg, "z")
}

func TestRenderCoalescedWithTemplate(t *testing.T) {
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	events := []Event{
		{Kind: KindModUpdateDetected, Instance: "a"},
		{Kind: KindModUpdateDetected, Instance: "b"},
		{Kind: KindModUpdateDetected, Instance: "a"}, // duplicate instance
	}
	msg, err := ts.RenderCoalesced(KindModUpdateDetected, events)
	require.NoError(t, err, "RenderCoalesced")
	require.Contains(t, msg, "3")
	assert.Contains(t, msg, "a, b")
}

func TestRenderCoalescedFallsBackToJoinedLines(t *testing.T) {
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	events := []Event{
		{Kind: KindDriftDetected, Instance: "a", Data: map[string]any{"Path": "p1"}},
		{Kind: KindDriftDetected, Instance: "b", Data: map[string]any{"Path": "p2"}},
	}
	msg, err := ts.RenderCoalesced(KindDriftDetected, events)
	require.NoError(t, err, "RenderCoalesced")
	lines := strings.Split(msg, "\n")
	require.Len(t, lines, 2)
}

func TestRenderCoalescedEmptyEvents(t *testing.T) {
	ts, err := NewTemplateSet()
	require.NoError(t, err, "NewTemplateSet")
	msg, err := ts.RenderCoalesced(KindHealthUnhealthy, nil)
	require.NoError(t, err, "RenderCoalesced")
	assert.Empty(t, msg)
}
