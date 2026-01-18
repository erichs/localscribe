package record

import (
	"flag"
	"fmt"
	"io"

	"localscribe/internal/config"
)

// Flags holds parsed command-line flags.
type Flags struct {
	ConfigFile       string
	ServerURL        string
	APIKey           string
	OutputDir        string
	FilenameTemplate string
	OutputFile       string
	Gain             float64
	DeviceIndex      int
	VADPause         bool
	PauseThreshold   float64
	Debug            bool
	ListDevices      bool
	ShowVersion      bool

	// Metadata flags
	HeartbeatInterval     int
	ZoomDetection         bool
	MeetDetection         bool
	CalendarIntegration   bool
	GoogleCredentialsFile string

	// Track which flags were explicitly set
	hasGain                  bool
	hasDeviceIndex           bool
	hasVADPause              bool
	hasPauseThreshold        bool
	hasDebug                 bool
	hasHeartbeatInterval     bool
	hasZoomDetection         bool
	hasMeetDetection         bool
	hasCalendarIntegration   bool
	hasGoogleCredentialsFile bool
}

// Usage prints help for the record subcommand.
func Usage(w io.Writer) {
	fs, _ := newFlagSet()
	fs.SetOutput(w)
	fmt.Fprintln(w, "Usage: localscribe record [flags]")
	fmt.Fprintln(w)
	fs.PrintDefaults()
}

func parseFlags(args []string) (*Flags, error) {
	fs, f := newFlagSet()
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// Track which flags were explicitly set by checking if they differ from defaults.
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "gain", "g":
			f.hasGain = true
		case "device":
			f.hasDeviceIndex = true
		case "vad-pause":
			f.hasVADPause = true
		case "pause-threshold":
			f.hasPauseThreshold = true
		case "debug":
			f.hasDebug = true
		case "heartbeat":
			f.hasHeartbeatInterval = true
		case "zoom":
			f.hasZoomDetection = true
		case "meet":
			f.hasMeetDetection = true
		case "calendar":
			f.hasCalendarIntegration = true
		case "google-creds":
			f.hasGoogleCredentialsFile = true
		}
	})

	return f, nil
}

func newFlagSet() (*flag.FlagSet, *Flags) {
	f := &Flags{}
	fs := flag.NewFlagSet("localscribe record", flag.ContinueOnError)

	fs.StringVar(&f.ConfigFile, "config", "", "Path to config file")
	fs.StringVar(&f.ConfigFile, "c", "", "Path to config file (shorthand)")

	fs.StringVar(&f.ServerURL, "server", "", "WebSocket server URL")
	fs.StringVar(&f.ServerURL, "s", "", "WebSocket server URL (shorthand)")

	fs.StringVar(&f.APIKey, "api-key", "", "API key for authentication")

	fs.StringVar(&f.OutputDir, "output-dir", "", "Output directory for transcripts")
	fs.StringVar(&f.OutputDir, "d", "", "Output directory (shorthand)")

	fs.StringVar(&f.FilenameTemplate, "template", "", "Filename template (e.g., transcript_%Y%m%d_%H%M%S.txt)")
	fs.StringVar(&f.FilenameTemplate, "t", "", "Filename template (shorthand)")

	fs.StringVar(&f.OutputFile, "output", "", "Output file path (overrides template)")
	fs.StringVar(&f.OutputFile, "o", "", "Output file path (shorthand)")

	fs.Float64Var(&f.Gain, "gain", 1.0, "Audio gain multiplier")
	fs.Float64Var(&f.Gain, "g", 1.0, "Audio gain (shorthand)")

	fs.IntVar(&f.DeviceIndex, "device", -1, "Audio input device index")

	fs.BoolVar(&f.VADPause, "vad-pause", false, "Pause on VAD end-of-turn detection")

	fs.Float64Var(&f.PauseThreshold, "pause-threshold", 2.0, "Silence threshold for line break (seconds)")

	fs.BoolVar(&f.Debug, "debug", false, "Enable debug output")

	fs.BoolVar(&f.ListDevices, "list-devices", false, "List available audio devices")
	fs.BoolVar(&f.ListDevices, "l", false, "List devices (shorthand)")

	fs.BoolVar(&f.ShowVersion, "version", false, "Show version")
	fs.BoolVar(&f.ShowVersion, "v", false, "Show version (shorthand)")

	// Metadata flags
	fs.IntVar(&f.HeartbeatInterval, "heartbeat", 60, "Heartbeat timestamp interval in seconds (0 to disable)")
	fs.BoolVar(&f.ZoomDetection, "zoom", false, "Enable Zoom meeting detection")
	fs.BoolVar(&f.MeetDetection, "meet", false, "Enable Google Meet detection")
	fs.BoolVar(&f.CalendarIntegration, "calendar", false, "Enable Google Calendar integration")
	fs.StringVar(&f.GoogleCredentialsFile, "google-creds", "", "Path to Google OAuth credentials file")

	return fs, f
}

// ToOverrides converts flags to config overrides.
func (f *Flags) ToOverrides() *config.FlagOverrides {
	return &config.FlagOverrides{
		ServerURL:                f.ServerURL,
		APIKey:                   f.APIKey,
		OutputDir:                f.OutputDir,
		FilenameTemplate:         f.FilenameTemplate,
		OutputFile:               f.OutputFile,
		Gain:                     f.Gain,
		DeviceIndex:              f.DeviceIndex,
		VADPause:                 f.VADPause,
		PauseThreshold:           f.PauseThreshold,
		Debug:                    f.Debug,
		HeartbeatInterval:        f.HeartbeatInterval,
		ZoomDetection:            f.ZoomDetection,
		MeetDetection:            f.MeetDetection,
		CalendarIntegration:      f.CalendarIntegration,
		GoogleCredentialsFile:    f.GoogleCredentialsFile,
		HasGain:                  f.hasGain,
		HasDeviceIndex:           f.hasDeviceIndex,
		HasVADPause:              f.hasVADPause,
		HasPauseThreshold:        f.hasPauseThreshold,
		HasDebug:                 f.hasDebug,
		HasHeartbeatInterval:     f.hasHeartbeatInterval,
		HasZoomDetection:         f.hasZoomDetection,
		HasMeetDetection:         f.hasMeetDetection,
		HasCalendarIntegration:   f.hasCalendarIntegration,
		HasGoogleCredentialsFile: f.hasGoogleCredentialsFile,
	}
}
