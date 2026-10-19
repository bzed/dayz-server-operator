// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package instance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bzed/dayz-server-operator/internal/quadlet"
)

func TestWriteContainerUnit(t *testing.T) {
	dir := t.TempDir()
	spec := quadlet.ContainerSpec{Name: "dzo-deerisle", Image: "localhost/dzo-runtime:latest", Network: quadlet.NetworkHost}

	path, err := WriteContainerUnit(dir, spec)
	require.NoError(t, err, "WriteContainerUnit")
	assert.Equal(t, filepath.Join(dir, "dzo-deerisle.container"), path)
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read")
	assert.Contains(t, string(data), "Image=localhost/dzo-runtime:latest")
}

func TestWriteContainerUnitInvalidSpecErrors(t *testing.T) {
	_, err := WriteContainerUnit(t.TempDir(), quadlet.ContainerSpec{})
	require.Error(t, err)
}

func TestWriteContainerUnitMissingDirErrors(t *testing.T) {
	spec := quadlet.ContainerSpec{Name: "dzo-x", Image: "img", Network: quadlet.NetworkHost}
	_, err := WriteContainerUnit(filepath.Join(t.TempDir(), "missing"), spec)
	require.Error(t, err)
}

func TestWriteContainerUnitOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	spec1 := quadlet.ContainerSpec{Name: "dzo-x", Image: "img1", Network: quadlet.NetworkHost}
	spec2 := quadlet.ContainerSpec{Name: "dzo-x", Image: "img2", Network: quadlet.NetworkHost}

	path, err := WriteContainerUnit(dir, spec1)
	require.NoError(t, err, "WriteContainerUnit")
	_, err = WriteContainerUnit(dir, spec2)
	require.NoError(t, err, "WriteContainerUnit (overwrite)")
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read")
	assert.Contains(t, string(data), "img2")
}

func TestTimerUnitRenderAndWrite(t *testing.T) {
	dir := t.TempDir()
	unit := TimerUnit{
		Name:        "dzo-restart-deerisle",
		Description: "Maintenance restart for deerisle",
		ExecStart:   "/usr/local/bin/dzo restart deerisle --graceful",
		OnCalendar:  []string{"Mon..Fri 04:00", "Sat,Sun 06:00"},
		Persistent:  false,
	}
	timerPath, servicePath, err := WriteTimerUnit(dir, unit)
	require.NoError(t, err, "WriteTimerUnit")

	timerData, err := os.ReadFile(timerPath) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read timer")
	require.Contains(t, string(timerData), "OnCalendar=Mon..Fri 04:00")
	require.Contains(t, string(timerData), "OnCalendar=Sat,Sun 06:00")
	require.Contains(t, string(timerData), "Persistent=false")
	assert.Contains(t, string(timerData), "WantedBy=timers.target")

	serviceData, err := os.ReadFile(servicePath) //nolint:gosec // test fixture path under t.TempDir()
	require.NoError(t, err, "read service")
	require.Contains(t, string(serviceData), "Type=oneshot")
	assert.Contains(t, string(serviceData), "ExecStart=/usr/local/bin/dzo restart deerisle --graceful")
}

func TestTimerUnitValidate(t *testing.T) {
	cases := []TimerUnit{
		{},
		{Name: "x"},
		{Name: "x", ExecStart: "cmd"},
	}
	for _, u := range cases {
		err := u.Validate()
		require.Error(t, err)
	}
	ok := TimerUnit{Name: "x", ExecStart: "cmd", OnCalendar: []string{"daily"}}
	err := ok.Validate()
	require.NoError(t, err)
}

func TestWriteTimerUnitMissingDirErrors(t *testing.T) {
	unit := TimerUnit{Name: "x", ExecStart: "cmd", OnCalendar: []string{"daily"}}
	_, _, err := WriteTimerUnit(filepath.Join(t.TempDir(), "missing"), unit)
	require.Error(t, err)
}

func TestTimerUnitNoDescriptionOmitsUnitSection(t *testing.T) {
	unit := TimerUnit{Name: "x", ExecStart: "cmd", OnCalendar: []string{"daily"}}
	timer, err := unit.RenderTimer()
	require.NoError(t, err, "RenderTimer")
	assert.NotContains(t, timer, "[Unit]")
}
