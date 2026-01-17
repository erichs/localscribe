// Package main provides the localdsmc CLI entry point.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"localdsmc/internal/audio"
	"localdsmc/internal/client"
	"localdsmc/internal/config"
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

	// Track which flags were explicitly set
	hasGain           bool
	hasDeviceIndex    bool
	hasVADPause       bool
	hasPauseThreshold bool
	hasDebug          bool
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
		}
	})

	return f, nil
}

// ToOverrides converts flags to config overrides.
func (f *Flags) ToOverrides() *config.FlagOverrides {
	return &config.FlagOverrides{
		ServerURL:         f.ServerURL,
		APIKey:            f.APIKey,
		OutputDir:         f.OutputDir,
		FilenameTemplate:  f.FilenameTemplate,
		OutputFile:        f.OutputFile,
		Gain:              f.Gain,
		DeviceIndex:       f.DeviceIndex,
		VADPause:          f.VADPause,
		PauseThreshold:    f.PauseThreshold,
		Debug:             f.Debug,
		HasGain:           f.hasGain,
		HasDeviceIndex:    f.hasDeviceIndex,
		HasVADPause:       f.hasVADPause,
		HasPauseThreshold: f.hasPauseThreshold,
		HasDebug:          f.hasDebug,
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
	fmt.Fprintf(stderr, "Press Ctrl+C to stop.\n\n")

	// Start audio capture
	if err := capture.Start(); err != nil {
		return fmt.Errorf("failed to start audio capture: %w", err)
	}

	// Create channels for coordination
	done := make(chan struct{})
	errChan := make(chan error, 2)

	// Goroutine to send audio to server
	go func() {
		for {
			select {
			case <-done:
				return
			case chunk, ok := <-capture.Chunks():
				if !ok {
					return
				}
				if err := wsClient.SendAudio(chunk); err != nil {
					errChan <- fmt.Errorf("send error: %w", err)
					return
				}
			}
		}
	}()

	// Goroutine to receive transcripts from server
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				msg, err := wsClient.Receive()
				if err != nil {
					if !wsClient.IsClosed() {
						errChan <- fmt.Errorf("receive error: %w", err)
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

	// Wait for signal or error
	select {
	case <-sigChan:
		fmt.Fprintf(stderr, "\nStopping...\n")
	case err := <-errChan:
		fmt.Fprintf(stderr, "\nError: %v\n", err)
	}

	// Clean shutdown
	close(done)
	capture.Stop()
	wsClient.Close()

	// Final newline and flush
	multiWriter.Write("\n")
	multiWriter.Flush()

	fmt.Fprintf(stderr, "Transcript saved to: %s\n", outputPath)
	return nil
}
