package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := Default()

	assert.Equal(t, "ws://127.0.0.1:8080", cfg.ServerURL)
	assert.Equal(t, "public_token", cfg.APIKey)
	assert.Equal(t, ".", cfg.OutputDir)
	assert.Equal(t, "transcript_%Y%m%d_%H%M%S.txt", cfg.FilenameTemplate)
	assert.Equal(t, float64(1.0), cfg.Gain)
	assert.Equal(t, -1, cfg.DeviceIndex)
	assert.False(t, cfg.VADPause)
	assert.Equal(t, 2.0, cfg.PauseThreshold)
	assert.False(t, cfg.Debug)
}

func TestLoadConfigFromFile(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
server_url: ws://localhost:9000
api_key: my_secret_key
output_dir: /tmp/transcripts
filename_template: recording_%Y%m%d.txt
gain: 2.5
device_index: 1
vad_pause: true
pause_threshold: 3.0
debug: true
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.Equal(t, "ws://localhost:9000", cfg.ServerURL)
	assert.Equal(t, "my_secret_key", cfg.APIKey)
	assert.Equal(t, "/tmp/transcripts", cfg.OutputDir)
	assert.Equal(t, "recording_%Y%m%d.txt", cfg.FilenameTemplate)
	assert.Equal(t, 2.5, cfg.Gain)
	assert.Equal(t, 1, cfg.DeviceIndex)
	assert.True(t, cfg.VADPause)
	assert.Equal(t, 3.0, cfg.PauseThreshold)
	assert.True(t, cfg.Debug)
}

func TestLoadConfigPartial(t *testing.T) {
	// Config file with only some values - should use defaults for others
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
output_dir: /my/output
gain: 3.0
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// Specified values
	assert.Equal(t, "/my/output", cfg.OutputDir)
	assert.Equal(t, 3.0, cfg.Gain)

	// Default values
	assert.Equal(t, "ws://127.0.0.1:8080", cfg.ServerURL)
	assert.Equal(t, "public_token", cfg.APIKey)
	assert.Equal(t, "transcript_%Y%m%d_%H%M%S.txt", cfg.FilenameTemplate)
}

func TestLoadConfigFileNotFound(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")

	// Should return default config when file not found
	require.NoError(t, err)
	assert.Equal(t, Default(), cfg)
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	err := os.WriteFile(configPath, []byte("invalid: yaml: content: ["), 0644)
	require.NoError(t, err)

	_, err = Load(configPath)
	assert.Error(t, err)
}

func TestExpandFilenameTemplate(t *testing.T) {
	// Use a fixed time for testing
	fixedTime := time.Date(2024, 3, 15, 14, 30, 45, 0, time.Local)

	tests := []struct {
		name     string
		template string
		expected string
	}{
		{
			name:     "full datetime",
			template: "transcript_%Y%m%d_%H%M%S.txt",
			expected: "transcript_20240315_143045.txt",
		},
		{
			name:     "date only",
			template: "recording_%Y-%m-%d.txt",
			expected: "recording_2024-03-15.txt",
		},
		{
			name:     "time only",
			template: "audio_%H%M.txt",
			expected: "audio_1430.txt",
		},
		{
			name:     "no template vars",
			template: "static_filename.txt",
			expected: "static_filename.txt",
		},
		{
			name:     "year and month",
			template: "%Y/%m/transcript.txt",
			expected: "2024/03/transcript.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExpandFilenameTemplate(tt.template, fixedTime)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConfigMergeWithFlags(t *testing.T) {
	base := Default()

	flags := &FlagOverrides{
		ServerURL:  "ws://custom:8080",
		OutputDir:  "/custom/output",
		Gain:       5.0,
		Debug:      true,
		HasGain:    true,
		HasDebug:   true,
	}

	merged := base.MergeFlags(flags)

	// Overridden values
	assert.Equal(t, "ws://custom:8080", merged.ServerURL)
	assert.Equal(t, "/custom/output", merged.OutputDir)
	assert.Equal(t, 5.0, merged.Gain)
	assert.True(t, merged.Debug)

	// Non-overridden values (from default)
	assert.Equal(t, "public_token", merged.APIKey)
	assert.Equal(t, "transcript_%Y%m%d_%H%M%S.txt", merged.FilenameTemplate)
}

func TestConfigMergeWithEmptyFlags(t *testing.T) {
	base := &Config{
		ServerURL:        "ws://original:8080",
		APIKey:           "original_key",
		OutputDir:        "/original/dir",
		FilenameTemplate: "original_%Y.txt",
		Gain:             2.0,
		DeviceIndex:      3,
		VADPause:         true,
		PauseThreshold:   4.0,
		Debug:            true,
	}

	flags := &FlagOverrides{} // Empty - no overrides

	merged := base.MergeFlags(flags)

	// All values should remain from base
	assert.Equal(t, base.ServerURL, merged.ServerURL)
	assert.Equal(t, base.APIKey, merged.APIKey)
	assert.Equal(t, base.OutputDir, merged.OutputDir)
	assert.Equal(t, base.FilenameTemplate, merged.FilenameTemplate)
	assert.Equal(t, base.Gain, merged.Gain)
	assert.Equal(t, base.DeviceIndex, merged.DeviceIndex)
	assert.Equal(t, base.VADPause, merged.VADPause)
	assert.Equal(t, base.PauseThreshold, merged.PauseThreshold)
	assert.Equal(t, base.Debug, merged.Debug)
}

func TestFindConfigFile(t *testing.T) {
	// Test that FindConfigFile looks in expected locations
	tmpDir := t.TempDir()

	// Create a config file in the temp dir
	configPath := filepath.Join(tmpDir, ".localscribe.yaml")
	err := os.WriteFile(configPath, []byte("output_dir: /test"), 0644)
	require.NoError(t, err)

	// FindConfigFile with explicit path
	found := FindConfigFile(configPath)
	assert.Equal(t, configPath, found)

	// FindConfigFile with empty string should return empty (no default found in test env)
	found = FindConfigFile("")
	// This will be empty or a real path depending on the system
	// We just verify it doesn't panic
	_ = found
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "tilde with path",
			input:    "~/transcripts",
			expected: filepath.Join(home, "transcripts"),
		},
		{
			name:     "tilde with nested path",
			input:    "~/documents/recordings/2024",
			expected: filepath.Join(home, "documents/recordings/2024"),
		},
		{
			name:     "tilde only",
			input:    "~",
			expected: home,
		},
		{
			name:     "absolute path unchanged",
			input:    "/tmp/transcripts",
			expected: "/tmp/transcripts",
		},
		{
			name:     "relative path unchanged",
			input:    "./transcripts",
			expected: "./transcripts",
		},
		{
			name:     "current dir unchanged",
			input:    ".",
			expected: ".",
		},
		{
			name:     "tilde in middle unchanged",
			input:    "/tmp/~user/transcripts",
			expected: "/tmp/~user/transcripts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExpandPath(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetOutputPath(t *testing.T) {
	cfg := &Config{
		OutputDir:        "/tmp/transcripts",
		FilenameTemplate: "recording_%Y%m%d.txt",
	}

	fixedTime := time.Date(2024, 3, 15, 14, 30, 45, 0, time.Local)
	path := cfg.GetOutputPath(fixedTime)

	assert.Equal(t, "/tmp/transcripts/recording_20240315.txt", path)
}

func TestGetOutputPathWithTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	cfg := &Config{
		OutputDir:        "~/transcripts",
		FilenameTemplate: "recording_%Y%m%d.txt",
	}

	fixedTime := time.Date(2024, 3, 15, 14, 30, 45, 0, time.Local)
	path := cfg.GetOutputPath(fixedTime)

	expected := filepath.Join(home, "transcripts", "recording_20240315.txt")
	assert.Equal(t, expected, path)
}

func TestGetOutputPathCurrentDir(t *testing.T) {
	cfg := &Config{
		OutputDir:        ".",
		FilenameTemplate: "transcript.txt",
	}

	path := cfg.GetOutputPath(time.Now())
	assert.Equal(t, "transcript.txt", path)
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		expectErr bool
		errMsg    string
	}{
		{
			name:      "valid config",
			config:    Default(),
			expectErr: false,
		},
		{
			name: "invalid gain - zero",
			config: &Config{
				ServerURL: "ws://localhost:8080",
				Gain:      0,
			},
			expectErr: true,
			errMsg:    "gain must be positive",
		},
		{
			name: "invalid gain - negative",
			config: &Config{
				ServerURL: "ws://localhost:8080",
				Gain:      -1.0,
			},
			expectErr: true,
			errMsg:    "gain must be positive",
		},
		{
			name: "invalid server URL - empty",
			config: &Config{
				ServerURL: "",
				Gain:      1.0,
			},
			expectErr: true,
			errMsg:    "server URL is required",
		},
		{
			name: "invalid pause threshold",
			config: &Config{
				ServerURL:      "ws://localhost:8080",
				Gain:           1.0,
				PauseThreshold: -1.0,
			},
			expectErr: true,
			errMsg:    "pause threshold must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadConfigWithPlugins(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
server_url: ws://localhost:8080
api_key: test
gain: 1.0

metadata:
  heartbeat_interval: 60
  plugins:
    - name: "git-branch"
      command: "git rev-parse --abbrev-ref HEAD"
      trigger: on_start
      timeout: 5s

    - name: "pomodoro"
      command: "echo 'Take a break!'"
      trigger: periodic
      interval: 1500
      timeout: 3s

    - name: "meeting-logger"
      command: "echo 'Meeting started'"
      trigger: on_meeting_start
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	assert.Len(t, cfg.Metadata.Plugins, 3)

	// Check first plugin
	p1 := cfg.Metadata.Plugins[0]
	assert.Equal(t, "git-branch", p1.Name)
	assert.Equal(t, "git rev-parse --abbrev-ref HEAD", p1.Command)
	assert.Equal(t, TriggerOnStart, p1.Trigger)
	assert.Equal(t, 5*time.Second, p1.Timeout.Duration())

	// Check periodic plugin
	p2 := cfg.Metadata.Plugins[1]
	assert.Equal(t, "pomodoro", p2.Name)
	assert.Equal(t, TriggerPeriodic, p2.Trigger)
	assert.Equal(t, 1500, p2.Interval)
	assert.Equal(t, 3*time.Second, p2.Timeout.Duration())

	// Check meeting plugin
	p3 := cfg.Metadata.Plugins[2]
	assert.Equal(t, "meeting-logger", p3.Name)
	assert.Equal(t, TriggerOnMeetingStart, p3.Trigger)
}

func TestDuration_UnmarshalYAML(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name     string
		yaml     string
		expected time.Duration
	}{
		{
			name:     "duration string seconds",
			yaml:     "metadata:\n  plugins:\n    - name: test\n      timeout: 5s",
			expected: 5 * time.Second,
		},
		{
			name:     "duration string minutes",
			yaml:     "metadata:\n  plugins:\n    - name: test\n      timeout: 2m",
			expected: 2 * time.Minute,
		},
		{
			name:     "duration string milliseconds",
			yaml:     "metadata:\n  plugins:\n    - name: test\n      timeout: 500ms",
			expected: 500 * time.Millisecond,
		},
		{
			name:     "integer as seconds",
			yaml:     "metadata:\n  plugins:\n    - name: test\n      timeout: 10",
			expected: 10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(tmpDir, tt.name+".yaml")
			fullYaml := "server_url: ws://localhost:8080\ngain: 1.0\n" + tt.yaml
			err := os.WriteFile(configPath, []byte(fullYaml), 0644)
			require.NoError(t, err)

			cfg, err := Load(configPath)
			require.NoError(t, err)
			require.Len(t, cfg.Metadata.Plugins, 1)

			assert.Equal(t, tt.expected, cfg.Metadata.Plugins[0].Timeout.Duration())
		})
	}
}

func TestTriggerTypes(t *testing.T) {
	// Verify trigger type constants
	assert.Equal(t, TriggerType("on_start"), TriggerOnStart)
	assert.Equal(t, TriggerType("on_meeting_start"), TriggerOnMeetingStart)
	assert.Equal(t, TriggerType("on_meeting_end"), TriggerOnMeetingEnd)
	assert.Equal(t, TriggerType("periodic"), TriggerPeriodic)
}
