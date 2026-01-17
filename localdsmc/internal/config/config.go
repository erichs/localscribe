// Package config handles configuration loading and merging for localdsmc.
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

	// Has* fields indicate whether the flag was explicitly set
	HasGain           bool
	HasDeviceIndex    bool
	HasVADPause       bool
	HasPauseThreshold bool
	HasDebug          bool
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
// Otherwise searches in: current dir, ~/.config/localdsmc/, ~/
func FindConfigFile(explicitPath string) string {
	if explicitPath != "" {
		return explicitPath
	}

	// Search locations in order of preference
	locations := []string{
		".localdsmc.yaml",
		".localdsmc.yml",
	}

	// Add home directory locations
	if home, err := os.UserHomeDir(); err == nil {
		locations = append(locations,
			filepath.Join(home, ".config", "localdsmc", "config.yaml"),
			filepath.Join(home, ".config", "localdsmc", "config.yml"),
			filepath.Join(home, ".localdsmc.yaml"),
			filepath.Join(home, ".localdsmc.yml"),
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
