// Package main provides the localdsmc CLI entry point.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"localdsmc/internal/audio"
	"localdsmc/internal/client"
	"localdsmc/internal/config"
	"localdsmc/internal/meetings"
	"localdsmc/internal/processor"
	"localdsmc/internal/writer"
)

var (
	version = "dev"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags, err := parseFlags(args)
	if err != nil {
		return err
	}

	// Handle special commands
	if flags.ShowVersion {
		fmt.Fprintf(stdout, "localdsmc version %s\n", version)
		return nil
	}

	if flags.ListDevices {
		return listDevices(stdout)
	}

	// Load config
	configPath := config.FindConfigFile(flags.ConfigFile)
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Merge CLI flags into config
	cfg = cfg.MergeFlags(flags.ToOverrides())

	// Validate config
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	if cfg.Debug {
		fmt.Fprintf(stderr, "[DEBUG] Config: server=%s, output_dir=%s, template=%s\n",
			cfg.ServerURL, cfg.OutputDir, cfg.FilenameTemplate)
	}

	return runTranscription(cfg, stdout, stderr)
}

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
	hasGain                   bool
	hasDeviceIndex            bool
	hasVADPause               bool
	hasPauseThreshold         bool
	hasDebug                  bool
	hasHeartbeatInterval      bool
	hasZoomDetection          bool
	hasMeetDetection          bool
	hasCalendarIntegration    bool
	hasGoogleCredentialsFile  bool
}

func parseFlags(args []string) (*Flags, error) {
	f := &Flags{}
	fs := flag.NewFlagSet("localdsmc", flag.ContinueOnError)

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

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// Track which flags were explicitly set by checking if they differ from defaults
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

func listDevices(w io.Writer) error {
	devices, err := audio.ListDevices()
	if err != nil {
		return fmt.Errorf("failed to list devices: %w", err)
	}

	fmt.Fprintln(w, "Available audio input devices:")
	fmt.Fprintln(w)
	for _, d := range devices {
		fmt.Fprintln(w, " ", d.String())
	}

	return nil
}

func runTranscription(cfg *config.Config, stdout, stderr io.Writer) error {
	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Set up signal handling for pause/resume (Ctrl+Z)
	pauseChan := make(chan os.Signal, 1)
	signal.Notify(pauseChan, syscall.SIGTSTP)

	// Pause state
	var pauseMu sync.Mutex
	paused := false

	// Determine output file path
	outputPath := cfg.GetOutputPath(time.Now())

	// Create output writer
	fileWriter, err := writer.New(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", outputPath, err)
	}
	defer fileWriter.Close()

	// Create multi-writer for stdout and file
	multiWriter := writer.NewMultiWriter(fileWriter, stdout)

	// Create post-processor
	postProc := processor.New(processor.Options{
		PauseThreshold: time.Duration(cfg.PauseThreshold * float64(time.Second)),
		MaxLineLength:  80,
	})

	// Create audio capture
	capture, err := audio.NewCapture(cfg.DeviceIndex, cfg.Gain)
	if err != nil {
		return fmt.Errorf("failed to initialize audio capture: %w", err)
	}
	defer capture.Close()

	// Connect to server
	fmt.Fprintf(stderr, "Connecting to %s...\n", cfg.ServerURL)
	wsClient, err := client.Connect(cfg.ServerURL, cfg.APIKey)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer wsClient.Close()

	fmt.Fprintf(stderr, "Connected. Transcribing to: %s\n", outputPath)
	fmt.Fprintf(stderr, "Press Ctrl+Z to pause/resume, Ctrl+C to stop.\n\n")

	// Start heartbeat timestamp goroutine if enabled
	heartbeatDone := make(chan struct{})
	if cfg.Metadata.HeartbeatInterval > 0 {
		go func() {
			ticker := time.NewTicker(time.Duration(cfg.Metadata.HeartbeatInterval) * time.Second)
			defer ticker.Stop()

			// Write initial timestamp
			ts := time.Now().Format("2006/01/02 15:04:05 MST")
			multiWriter.WriteMetadata(fmt.Sprintf("%%%% time: %s\n", ts))

			for {
				select {
				case <-heartbeatDone:
					return
				case t := <-ticker.C:
					ts := t.Format("2006/01/02 15:04:05 MST")
					multiWriter.WriteMetadata(fmt.Sprintf("%%%% time: %s\n", ts))
				}
			}
		}()
	}

	// Start combined meeting detection if Zoom or Meet detection is enabled
	var meetingDetectorCancel context.CancelFunc
	if cfg.Metadata.ZoomDetection || cfg.Metadata.MeetDetection {
		var meetingCtx context.Context
		meetingCtx, meetingDetectorCancel = context.WithCancel(context.Background())

		detector := meetings.NewDetector(
			func(info meetings.MeetingInfo) {
				// Meeting started
				ts := info.StartTime.Format("2006/01/02 15:04:05 MST")
				switch info.Type {
				case meetings.MeetingTypeZoom:
					if cfg.Metadata.ZoomDetection {
						multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting started: %s zoom\n", ts))
					}
				case meetings.MeetingTypeMeet:
					if cfg.Metadata.MeetDetection {
						if info.Title != "" {
							multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting started: %s meet/%s\n%%%% meeting title: %s\n", ts, info.Code, info.Title))
						} else if info.Code != "" {
							multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting started: %s meet/%s\n", ts, info.Code))
						} else {
							multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting started: %s meet\n", ts))
						}
					}
				}
			},
			func(meetingType meetings.MeetingType, duration time.Duration) {
				// Meeting ended
				ts := time.Now().Format("2006/01/02 15:04:05 MST")
				mins := meetings.RoundToNearestMinute(duration)
				switch meetingType {
				case meetings.MeetingTypeZoom:
					if cfg.Metadata.ZoomDetection {
						multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting ended: %s zoom (duration: %dm)\n", ts, mins))
					}
				case meetings.MeetingTypeMeet:
					if cfg.Metadata.MeetDetection {
						multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting ended: %s meet (duration: %dm)\n", ts, mins))
					}
				}
			},
		)

		go func() {
			if err := detector.Start(meetingCtx); err != nil && cfg.Debug {
				fmt.Fprintf(stderr, "[DEBUG] Meeting detection error: %v\n", err)
			}
		}()
	}

	// Start audio capture
	if err := capture.Start(); err != nil {
		return fmt.Errorf("failed to start audio capture: %w", err)
	}

	// Create channels for coordination
	done := make(chan struct{})

	// Reconnecting state
	var reconnectMu sync.Mutex
	reconnecting := false

	// Helper to check if paused or reconnecting
	shouldSkip := func() bool {
		pauseMu.Lock()
		p := paused
		pauseMu.Unlock()
		reconnectMu.Lock()
		r := reconnecting
		reconnectMu.Unlock()
		return p || r
	}

	// Goroutine to handle pause/resume signal (Ctrl+Z)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-pauseChan:
				pauseMu.Lock()
				paused = !paused
				if paused {
					fmt.Fprintf(stderr, "\n[PAUSED] Press Ctrl+Z to resume\n")
				} else {
					fmt.Fprintf(stderr, "[RESUMED]\n")
				}
				pauseMu.Unlock()
			}
		}
	}()

	// Start send/receive goroutines
	startWorkers := func() (chan struct{}, chan error) {
		workerDone := make(chan struct{})
		workerErr := make(chan error, 2)

		// Goroutine to send audio to server
		go func() {
			for {
				select {
				case <-workerDone:
					return
				case <-done:
					return
				case chunk, ok := <-capture.Chunks():
					if !ok {
						return
					}
					// Skip sending when paused or reconnecting
					if shouldSkip() {
						continue
					}
					if err := wsClient.SendAudio(chunk); err != nil {
						if !wsClient.IsClosed() {
							workerErr <- fmt.Errorf("send error: %w", err)
						}
						return
					}
				}
			}
		}()

		// Goroutine to receive transcripts from server
		go func() {
			for {
				select {
				case <-workerDone:
					return
				case <-done:
					return
				default:
					msg, err := wsClient.Receive()
					if err != nil {
						if !wsClient.IsClosed() {
							workerErr <- fmt.Errorf("receive error: %w", err)
						}
						return
					}

					switch m := msg.(type) {
					case *client.WordMessage:
						output := postProc.ProcessWord(m.Text)
						if output != "" {
							multiWriter.Write(output)
						}

					case *client.StepMessage:
						if m.IsEndOfTurn() {
							if cfg.Debug {
								fmt.Fprintf(stderr, "[DEBUG] End of turn detected\n")
							}
							output := postProc.ProcessEndOfTurn()
							if output != "" {
								multiWriter.Write(output)
							}
						}
					}
				}
			}
		}()

		return workerDone, workerErr
	}

	workerDone, workerErr := startWorkers()

	// Main loop with reconnection handling
	for {
		select {
		case <-sigChan:
			fmt.Fprintf(stderr, "\nStopping...\n")
			goto shutdown

		case err := <-workerErr:
			fmt.Fprintf(stderr, "\nConnection error: %v\n", err)

			// Set reconnecting state
			reconnectMu.Lock()
			reconnecting = true
			reconnectMu.Unlock()

			// Stop current workers
			close(workerDone)

			// Attempt reconnection
			fmt.Fprintf(stderr, "Attempting to reconnect...\n")
			reconnectErr := wsClient.Reconnect(0, func(attempt int, delay time.Duration) {
				fmt.Fprintf(stderr, "  Reconnection attempt %d (waiting %v)...\n", attempt, delay)
			})

			if reconnectErr != nil {
				fmt.Fprintf(stderr, "Reconnection failed: %v\n", reconnectErr)
				goto shutdown
			}

			fmt.Fprintf(stderr, "Reconnected successfully.\n")

			// Clear reconnecting state and restart workers
			reconnectMu.Lock()
			reconnecting = false
			reconnectMu.Unlock()

			workerDone, workerErr = startWorkers()
		}
	}

shutdown:
	// Clean shutdown
	close(done)
	close(heartbeatDone)
	if meetingDetectorCancel != nil {
		meetingDetectorCancel()
	}
	// Close workerDone safely (it might already be closed during reconnection)
	select {
	case <-workerDone:
		// Already closed
	default:
		close(workerDone)
	}
	capture.Stop()
	wsClient.Close()

	// Final newline and flush
	multiWriter.Write("\n")
	multiWriter.Flush()

	fmt.Fprintf(stderr, "Transcript saved to: %s\n", outputPath)
	return nil
}
