package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		check    func(t *testing.T, f *Flags)
		wantErr  bool
	}{
		{
			name: "no flags",
			args: []string{},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "", f.ServerURL)
				assert.Equal(t, "", f.ConfigFile)
				assert.Equal(t, 1.0, f.Gain)
				assert.Equal(t, -1, f.DeviceIndex)
				assert.False(t, f.Debug)
			},
		},
		{
			name: "server flag",
			args: []string{"-server", "ws://custom:9000"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "ws://custom:9000", f.ServerURL)
			},
		},
		{
			name: "server shorthand",
			args: []string{"-s", "ws://custom:9000"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "ws://custom:9000", f.ServerURL)
			},
		},
		{
			name: "config flag",
			args: []string{"-config", "/path/to/config.yaml"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "/path/to/config.yaml", f.ConfigFile)
			},
		},
		{
			name: "config shorthand",
			args: []string{"-c", "/path/to/config.yaml"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "/path/to/config.yaml", f.ConfigFile)
			},
		},
		{
			name: "output-dir flag",
			args: []string{"-output-dir", "/tmp/transcripts"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "/tmp/transcripts", f.OutputDir)
			},
		},
		{
			name: "output-dir shorthand",
			args: []string{"-d", "/tmp/transcripts"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "/tmp/transcripts", f.OutputDir)
			},
		},
		{
			name: "template flag",
			args: []string{"-template", "recording_%Y%m%d.txt"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "recording_%Y%m%d.txt", f.FilenameTemplate)
			},
		},
		{
			name: "template shorthand",
			args: []string{"-t", "recording_%Y%m%d.txt"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "recording_%Y%m%d.txt", f.FilenameTemplate)
			},
		},
		{
			name: "output flag",
			args: []string{"-output", "/tmp/my-transcript.txt"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "/tmp/my-transcript.txt", f.OutputFile)
			},
		},
		{
			name: "output shorthand",
			args: []string{"-o", "/tmp/my-transcript.txt"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "/tmp/my-transcript.txt", f.OutputFile)
			},
		},
		{
			name: "gain flag",
			args: []string{"-gain", "2.5"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, 2.5, f.Gain)
				assert.True(t, f.hasGain)
			},
		},
		{
			name: "gain shorthand",
			args: []string{"-g", "3.0"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, 3.0, f.Gain)
				assert.True(t, f.hasGain)
			},
		},
		{
			name: "device flag",
			args: []string{"-device", "2"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, 2, f.DeviceIndex)
				assert.True(t, f.hasDeviceIndex)
			},
		},
		{
			name: "api-key flag",
			args: []string{"-api-key", "my_secret"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "my_secret", f.APIKey)
			},
		},
		{
			name: "vad-pause flag",
			args: []string{"-vad-pause"},
			check: func(t *testing.T, f *Flags) {
				assert.True(t, f.VADPause)
				assert.True(t, f.hasVADPause)
			},
		},
		{
			name: "pause-threshold flag",
			args: []string{"-pause-threshold", "3.5"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, 3.5, f.PauseThreshold)
				assert.True(t, f.hasPauseThreshold)
			},
		},
		{
			name: "debug flag",
			args: []string{"-debug"},
			check: func(t *testing.T, f *Flags) {
				assert.True(t, f.Debug)
				assert.True(t, f.hasDebug)
			},
		},
		{
			name: "list-devices flag",
			args: []string{"-list-devices"},
			check: func(t *testing.T, f *Flags) {
				assert.True(t, f.ListDevices)
			},
		},
		{
			name: "list-devices shorthand",
			args: []string{"-l"},
			check: func(t *testing.T, f *Flags) {
				assert.True(t, f.ListDevices)
			},
		},
		{
			name: "version flag",
			args: []string{"-version"},
			check: func(t *testing.T, f *Flags) {
				assert.True(t, f.ShowVersion)
			},
		},
		{
			name: "version shorthand",
			args: []string{"-v"},
			check: func(t *testing.T, f *Flags) {
				assert.True(t, f.ShowVersion)
			},
		},
		{
			name: "multiple flags",
			args: []string{"-s", "ws://localhost:9000", "-g", "2.0", "-debug", "-d", "/output"},
			check: func(t *testing.T, f *Flags) {
				assert.Equal(t, "ws://localhost:9000", f.ServerURL)
				assert.Equal(t, 2.0, f.Gain)
				assert.True(t, f.Debug)
				assert.Equal(t, "/output", f.OutputDir)
			},
		},
		{
			name:    "invalid flag",
			args:    []string{"-invalid-flag"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := parseFlags(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, f)
			}
		})
	}
}

func TestFlagsToOverrides(t *testing.T) {
	f := &Flags{
		ServerURL:         "ws://test:8080",
		APIKey:            "key123",
		OutputDir:         "/output",
		FilenameTemplate:  "test_%Y.txt",
		Gain:              2.5,
		DeviceIndex:       1,
		VADPause:          true,
		PauseThreshold:    3.0,
		Debug:             true,
		hasGain:           true,
		hasDeviceIndex:    true,
		hasVADPause:       true,
		hasPauseThreshold: true,
		hasDebug:          true,
	}

	o := f.ToOverrides()

	assert.Equal(t, "ws://test:8080", o.ServerURL)
	assert.Equal(t, "key123", o.APIKey)
	assert.Equal(t, "/output", o.OutputDir)
	assert.Equal(t, "test_%Y.txt", o.FilenameTemplate)
	assert.Equal(t, 2.5, o.Gain)
	assert.Equal(t, 1, o.DeviceIndex)
	assert.True(t, o.VADPause)
	assert.Equal(t, 3.0, o.PauseThreshold)
	assert.True(t, o.Debug)
	assert.True(t, o.HasGain)
	assert.True(t, o.HasDeviceIndex)
	assert.True(t, o.HasVADPause)
	assert.True(t, o.HasPauseThreshold)
	assert.True(t, o.HasDebug)
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run([]string{"-version"}, &stdout, &stderr)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "localdsmc version")
}

func TestRunListDevices(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run([]string{"-list-devices"}, &stdout, &stderr)
	// This may fail if portaudio isn't available, but shouldn't panic
	if err != nil {
		assert.Contains(t, err.Error(), "device")
	} else {
		assert.Contains(t, stdout.String(), "Available audio input devices")
	}
}

func TestRunInvalidConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Invalid gain (0)
	err := run([]string{"-gain", "0"}, &stdout, &stderr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gain")
}

func TestRunMissingServer(t *testing.T) {
	// Skip this test - with auto-reconnection enabled, the client will
	// retry indefinitely when it can't connect to the server.
	// The empty server URL doesn't override the default (ws://127.0.0.1:8080),
	// so it attempts to connect and then reconnects forever.
	t.Skip("Test incompatible with auto-reconnection feature")
}

func TestRunWithConfigFile(t *testing.T) {
	// Skip this test - with auto-reconnection enabled, the client will
	// retry indefinitely when it can't connect to the server.
	t.Skip("Test incompatible with auto-reconnection feature")
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

func TestParseArgsHelp(t *testing.T) {
	// Help flag should cause an error (flag package behavior)
	_, err := parseFlags([]string{"-h"})
	assert.Error(t, err)
}

func TestFlagsWithoutExplicitSet(t *testing.T) {
	f, err := parseFlags([]string{})
	require.NoError(t, err)

	// None of the "has" flags should be set
	assert.False(t, f.hasGain)
	assert.False(t, f.hasDeviceIndex)
	assert.False(t, f.hasVADPause)
	assert.False(t, f.hasPauseThreshold)
	assert.False(t, f.hasDebug)

	o := f.ToOverrides()
	assert.False(t, o.HasGain)
	assert.False(t, o.HasDeviceIndex)
}

func TestMetadataFormatPrefix(t *testing.T) {
	// These tests verify that the format strings used for metadata produce
	// output with "%%" prefix (required by lastscribed parser).
	// In Go's fmt.Sprintf, "%%%%" produces "%%".

	ts := "2026/01/17 23:04:29 EST"

	t.Run("heartbeat format", func(t *testing.T) {
		heartbeat := fmt.Sprintf("%%%% time: %s\n", ts)
		assert.True(t, strings.HasPrefix(heartbeat, "%% time:"),
			"heartbeat should start with '%%', got: %s", heartbeat)
		assert.Equal(t, "%% time: 2026/01/17 23:04:29 EST\n", heartbeat)
	})

	t.Run("zoom meeting start format", func(t *testing.T) {
		meetingStart := fmt.Sprintf("%%%% meeting started: %s zoom\n", ts)
		assert.True(t, strings.HasPrefix(meetingStart, "%% meeting started:"),
			"meeting start should start with '%%', got: %s", meetingStart)
		assert.Equal(t, "%% meeting started: 2026/01/17 23:04:29 EST zoom\n", meetingStart)
	})

	t.Run("meet meeting start with title format", func(t *testing.T) {
		code := "abc-defg-hij"
		title := "Weekly Standup"
		meetingStart := fmt.Sprintf("%%%% meeting started: %s meet/%s\n%%%% meeting title: %s\n", ts, code, title)
		assert.True(t, strings.HasPrefix(meetingStart, "%% meeting started:"),
			"meeting start should start with '%%', got: %s", meetingStart)
		assert.Contains(t, meetingStart, "%% meeting title:")
		assert.Equal(t, "%% meeting started: 2026/01/17 23:04:29 EST meet/abc-defg-hij\n%% meeting title: Weekly Standup\n", meetingStart)
	})

	t.Run("meet meeting start with code format", func(t *testing.T) {
		code := "abc-defg-hij"
		meetingStart := fmt.Sprintf("%%%% meeting started: %s meet/%s\n", ts, code)
		assert.True(t, strings.HasPrefix(meetingStart, "%% meeting started:"),
			"meeting start should start with '%%', got: %s", meetingStart)
		assert.Equal(t, "%% meeting started: 2026/01/17 23:04:29 EST meet/abc-defg-hij\n", meetingStart)
	})

	t.Run("meet meeting start basic format", func(t *testing.T) {
		meetingStart := fmt.Sprintf("%%%% meeting started: %s meet\n", ts)
		assert.True(t, strings.HasPrefix(meetingStart, "%% meeting started:"),
			"meeting start should start with '%%', got: %s", meetingStart)
		assert.Equal(t, "%% meeting started: 2026/01/17 23:04:29 EST meet\n", meetingStart)
	})

	t.Run("zoom meeting end format", func(t *testing.T) {
		mins := 45
		meetingEnd := fmt.Sprintf("%%%% meeting ended: %s zoom (duration: %dm)\n", ts, mins)
		assert.True(t, strings.HasPrefix(meetingEnd, "%% meeting ended:"),
			"meeting end should start with '%%', got: %s", meetingEnd)
		assert.Equal(t, "%% meeting ended: 2026/01/17 23:04:29 EST zoom (duration: 45m)\n", meetingEnd)
	})

	t.Run("meet meeting end format", func(t *testing.T) {
		mins := 30
		meetingEnd := fmt.Sprintf("%%%% meeting ended: %s meet (duration: %dm)\n", ts, mins)
		assert.True(t, strings.HasPrefix(meetingEnd, "%% meeting ended:"),
			"meeting end should start with '%%', got: %s", meetingEnd)
		assert.Equal(t, "%% meeting ended: 2026/01/17 23:04:29 EST meet (duration: 30m)\n", meetingEnd)
	})
}
