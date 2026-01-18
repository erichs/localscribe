// Package config handles configuration loading and merging for localscribe.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration values for the client.
type Config struct {
	ServerURL        string  `yaml:"server_url"`
	APIKey           string  `yaml:"api_key"`
	OutputDir        string  `yaml:"output_dir"`
	FilenameTemplate string  `yaml:"filename_template"`
	Gain             float64 `yaml:"gain"`
	DeviceIndex      int     `yaml:"device_index"`
	VADPause         bool    `yaml:"vad_pause"`
	PauseThreshold   float64 `yaml:"pause_threshold"`
	Debug            bool    `yaml:"debug"`

	// Metadata features
	Metadata MetadataConfig `yaml:"metadata"`
}

// MetadataConfig holds configuration for metadata collection features.
type MetadataConfig struct {
	// HeartbeatInterval is how often to write timestamp markers (in seconds).
	// Set to 0 to disable heartbeat timestamps.
	HeartbeatInterval int `yaml:"heartbeat_interval"`

	// ZoomDetection enables automatic Zoom meeting detection.
	ZoomDetection bool `yaml:"zoom_detection"`

	// MeetDetection enables automatic Google Meet detection.
	MeetDetection bool `yaml:"meet_detection"`

	// CalendarIntegration enables Google Calendar API for meeting metadata.
	CalendarIntegration bool `yaml:"calendar_integration"`

	// GoogleCredentialsFile is the path to Google OAuth credentials.
	GoogleCredentialsFile string `yaml:"google_credentials_file"`

	// Plugins is a list of external plugins to execute at various lifecycle events.
	Plugins []PluginConfig `yaml:"plugins"`
}

// PluginConfig defines an external plugin to execute.
type PluginConfig struct {
	// Name is a unique identifier for the plugin (used in metadata output).
	Name string `yaml:"name"`

	// Command is the shell command or script to execute.
	Command string `yaml:"command"`

	// Trigger determines when the plugin runs.
	// Valid values: on_start, on_meeting_start, on_meeting_end, periodic
	Trigger TriggerType `yaml:"trigger"`

	// Interval is the period between executions in seconds (only for periodic trigger).
	Interval int `yaml:"interval"`

	// Timeout is the maximum time the plugin can run before being killed.
	// Default: 5s. Format: Go duration string (e.g., "5s", "1m").
	Timeout Duration `yaml:"timeout"`
}

// TriggerType defines when a plugin should execute.
type TriggerType string

const (
	TriggerOnStart        TriggerType = "on_start"
	TriggerOnMeetingStart TriggerType = "on_meeting_start"
	TriggerOnMeetingEnd   TriggerType = "on_meeting_end"
	TriggerPeriodic       TriggerType = "periodic"
)

// Duration is a wrapper around time.Duration for YAML unmarshaling.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler for Duration.
func (d *Duration) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try parsing as an integer first (seconds)
	var secs int
	if err := unmarshal(&secs); err == nil {
		*d = Duration(time.Duration(secs) * time.Second)
		return nil
	}

	// Try parsing as a duration string (e.g., "5s", "2m")
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		*d = 0
		return nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(dur)
	return nil
}

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// FlagOverrides contains CLI flag values that override config file settings.
// Empty strings and false booleans are not considered overrides unless
// the corresponding Has* field is true.
type FlagOverrides struct {
	ServerURL        string
	APIKey           string
	OutputDir        string
	FilenameTemplate string
	Gain             float64
	DeviceIndex      int
	VADPause         bool
	PauseThreshold   float64
	Debug            bool
	OutputFile       string // Direct output file path (overrides template)

	// Metadata flags
	HeartbeatInterval     int
	ZoomDetection         bool
	MeetDetection         bool
	CalendarIntegration   bool
	GoogleCredentialsFile string

	// Has* fields indicate whether the flag was explicitly set
	HasGain                   bool
	HasDeviceIndex            bool
	HasVADPause               bool
	HasPauseThreshold         bool
	HasDebug                  bool
	HasHeartbeatInterval      bool
	HasZoomDetection          bool
	HasMeetDetection          bool
	HasCalendarIntegration    bool
	HasGoogleCredentialsFile  bool
}

// Default returns a Config with default values.
func Default() *Config {
	return &Config{
		ServerURL:        "ws://127.0.0.1:8080",
		APIKey:           "public_token",
		OutputDir:        ".",
		FilenameTemplate: "transcript_%Y%m%d_%H%M%S.txt",
		Gain:             1.0,
		DeviceIndex:      -1, // -1 means use default device
		VADPause:         false,
		PauseThreshold:   2.0,
		Debug:            false,
		Metadata: MetadataConfig{
			HeartbeatInterval:     60, // 1 minute default
			ZoomDetection:         false,
			MeetDetection:         false,
			CalendarIntegration:   false,
			GoogleCredentialsFile: "",
		},
	}
}

// Load reads configuration from a YAML file.
// If the file doesn't exist, returns default configuration.
// If the file exists but is invalid, returns an error.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// FindConfigFile searches for a config file in standard locations.
// If explicitPath is provided, returns it directly.
// Otherwise searches in: current dir, ~/.config/localscribe/, ~/
func FindConfigFile(explicitPath string) string {
	if explicitPath != "" {
		return explicitPath
	}

	// Search locations in order of preference
	locations := []string{
		".localscribe.yaml",
		".localscribe.yml",
	}

	// Add home directory locations
	if home, err := os.UserHomeDir(); err == nil {
		locations = append(locations,
			filepath.Join(home, ".config", "localscribe", "config.yaml"),
			filepath.Join(home, ".config", "localscribe", "config.yml"),
			filepath.Join(home, ".localscribe.yaml"),
			filepath.Join(home, ".localscribe.yml"),
		)
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc
		}
	}

	return ""
}

// MergeFlags creates a new Config with flag overrides applied.
// Empty string values in flags are not considered overrides.
// Boolean and numeric flags use Has* fields to determine if they were set.
func (c *Config) MergeFlags(flags *FlagOverrides) *Config {
	merged := *c // Copy the config
	merged.Metadata = c.Metadata // Ensure nested struct is copied

	if flags.ServerURL != "" {
		merged.ServerURL = flags.ServerURL
	}
	if flags.APIKey != "" {
		merged.APIKey = flags.APIKey
	}
	if flags.OutputDir != "" {
		merged.OutputDir = flags.OutputDir
	}
	if flags.FilenameTemplate != "" {
		merged.FilenameTemplate = flags.FilenameTemplate
	}
	if flags.HasGain {
		merged.Gain = flags.Gain
	}
	if flags.HasDeviceIndex {
		merged.DeviceIndex = flags.DeviceIndex
	}
	if flags.HasVADPause {
		merged.VADPause = flags.VADPause
	}
	if flags.HasPauseThreshold {
		merged.PauseThreshold = flags.PauseThreshold
	}
	if flags.HasDebug {
		merged.Debug = flags.Debug
	}

	// Metadata flags
	if flags.HasHeartbeatInterval {
		merged.Metadata.HeartbeatInterval = flags.HeartbeatInterval
	}
	if flags.HasZoomDetection {
		merged.Metadata.ZoomDetection = flags.ZoomDetection
	}
	if flags.HasMeetDetection {
		merged.Metadata.MeetDetection = flags.MeetDetection
	}
	if flags.HasCalendarIntegration {
		merged.Metadata.CalendarIntegration = flags.CalendarIntegration
	}
	if flags.HasGoogleCredentialsFile || flags.GoogleCredentialsFile != "" {
		merged.Metadata.GoogleCredentialsFile = flags.GoogleCredentialsFile
	}

	return &merged
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.ServerURL == "" {
		return errors.New("server URL is required")
	}
	if c.Gain <= 0 {
		return errors.New("gain must be positive")
	}
	if c.PauseThreshold < 0 {
		return errors.New("pause threshold must be non-negative")
	}
	return nil
}

// ExpandPath expands ~ to the user's home directory.
// This handles paths like "~/transcripts" or "~" itself.
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	return path
}

// ExpandFilenameTemplate expands date/time placeholders in the filename template.
// Supports strftime-like placeholders: %Y, %m, %d, %H, %M, %S
func ExpandFilenameTemplate(template string, t time.Time) string {
	replacements := map[string]string{
		"%Y": t.Format("2006"),
		"%m": t.Format("01"),
		"%d": t.Format("02"),
		"%H": t.Format("15"),
		"%M": t.Format("04"),
		"%S": t.Format("05"),
	}

	result := template
	for placeholder, value := range replacements {
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return result
}

// GetOutputPath returns the full output file path for the current time.
func (c *Config) GetOutputPath(t time.Time) string {
	filename := ExpandFilenameTemplate(c.FilenameTemplate, t)
	return filepath.Join(ExpandPath(c.OutputDir), filename)
}
