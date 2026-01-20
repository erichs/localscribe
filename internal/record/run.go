// Package record implements the localscribe recorder subcommand.
package record

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"localscribe/internal/audio"
	"localscribe/internal/client"
	"localscribe/internal/config"
	"localscribe/internal/diagnostics"
	"localscribe/internal/meetings"
	"localscribe/internal/plugins"
	"localscribe/internal/processor"
	"localscribe/internal/writer"
)

var (
	version = "dev"
)

// Run executes the record subcommand with the provided arguments.
func Run(args []string, stdout, stderr io.Writer) error {
	flags, err := parseFlags(args)
	if err != nil {
		return err
	}

	// Handle special commands
	if flags.ShowVersion {
		fmt.Fprintf(stdout, "localscribe version %s\n", version)
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
	stopChan := make(chan struct{})
	go func() {
		<-sigChan
		close(stopChan)
	}()

	// Set up signal handling for pause/resume (Ctrl+Z)
	pauseChan := make(chan os.Signal, 1)
	signal.Notify(pauseChan, syscall.SIGTSTP)

	// Set up signal handling for diagnostics (Ctrl+\)
	diagChan := make(chan os.Signal, 1)
	signal.Notify(diagChan, syscall.SIGQUIT)

	// Create diagnostic tracker
	diag := diagnostics.New()

	reconnectCtx, cancelReconnect := context.WithCancel(context.Background())
	defer cancelReconnect()
	go func() {
		<-stopChan
		cancelReconnect()
	}()

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
	logWriteErr := func(action string, err error) {
		if err != nil {
			fmt.Fprintf(stderr, "[WARN] %s: %v\n", action, err)
		}
	}

	// Create plugin runner
	pluginRunner := plugins.NewRunner(cfg.Metadata.Plugins, multiWriter, cfg.Debug, stderr)
	pluginCtx, pluginCancel := context.WithCancel(context.Background())
	defer pluginCancel()

	// Execute on_start plugins
	if pluginRunner.HasTrigger(config.TriggerOnStart) {
		if cfg.Debug {
			fmt.Fprintf(stderr, "[DEBUG] Executing on_start plugins...\n")
		}
		execCtx := &plugins.ExecuteContext{
			OutputFile: outputPath,
		}
		pluginRunner.Execute(pluginCtx, config.TriggerOnStart, execCtx)
	}

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
	diag.SetConnected(true, cfg.ServerURL)

	fmt.Fprintf(stderr, "Connected. Transcribing to: %s\n", outputPath)
	fmt.Fprintf(stderr, "Press Ctrl+Z to pause/resume, Ctrl+\\ for diagnostics, Ctrl+C to stop.\n\n")

	// Start heartbeat timestamp goroutine if enabled
	heartbeatDone := make(chan struct{})
	if cfg.Metadata.HeartbeatInterval > 0 {
		go func() {
			ticker := time.NewTicker(time.Duration(cfg.Metadata.HeartbeatInterval) * time.Second)
			defer ticker.Stop()

			// Write initial timestamp
			ts := time.Now().Format("2006/01/02 15:04:05 MST")
			if err := multiWriter.WriteMetadata(fmt.Sprintf("%%%% time: %s\n", ts)); err != nil {
				logWriteErr("failed to write heartbeat metadata", err)
			}

			for {
				select {
				case <-heartbeatDone:
					return
				case t := <-ticker.C:
					ts := t.Format("2006/01/02 15:04:05 MST")
					if err := multiWriter.WriteMetadata(fmt.Sprintf("%%%% time: %s\n", ts)); err != nil {
						logWriteErr("failed to write heartbeat metadata", err)
					}
				}
			}
		}()
	}

	// Start periodic plugins
	if pluginRunner.HasTrigger(config.TriggerPeriodic) {
		if cfg.Debug {
			fmt.Fprintf(stderr, "[DEBUG] Starting periodic plugins...\n")
		}
		execCtx := &plugins.ExecuteContext{
			OutputFile: outputPath,
		}
		pluginRunner.StartPeriodic(pluginCtx, execCtx)
	}

	// Start combined meeting detection if Zoom or Meet detection is enabled
	if cfg.Metadata.ZoomDetection || cfg.Metadata.MeetDetection {
		meetingCtx, meetingDetectorCancel := context.WithCancel(context.Background())
		defer meetingDetectorCancel()

		detector := meetings.NewDetector(
			func(info meetings.MeetingInfo) {
				// Meeting started
				ts := info.StartTime.Format("2006/01/02 15:04:05 MST")
				switch info.Type {
				case meetings.MeetingTypeZoom:
					if cfg.Metadata.ZoomDetection {
						if err := multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting started: %s zoom\n", ts)); err != nil {
							logWriteErr("failed to write zoom meeting start metadata", err)
						}
					}
				case meetings.MeetingTypeMeet:
					if cfg.Metadata.MeetDetection {
						if info.Title != "" {
							if err := multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting started: %s meet/%s\n%%%% meeting title: %s\n", ts, info.Code, info.Title)); err != nil {
								logWriteErr("failed to write meet meeting start metadata", err)
							}
						} else if info.Code != "" {
							if err := multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting started: %s meet/%s\n", ts, info.Code)); err != nil {
								logWriteErr("failed to write meet meeting start metadata", err)
							}
						} else {
							if err := multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting started: %s meet\n", ts)); err != nil {
								logWriteErr("failed to write meet meeting start metadata", err)
							}
						}
					}
				}

				// Execute on_meeting_start plugins
				if pluginRunner.HasTrigger(config.TriggerOnMeetingStart) {
					execCtx := &plugins.ExecuteContext{
						OutputFile:   outputPath,
						MeetingType:  info.Type,
						MeetingCode:  info.Code,
						MeetingTitle: info.Title,
					}
					go pluginRunner.Execute(pluginCtx, config.TriggerOnMeetingStart, execCtx)
				}
			},
			func(meetingType meetings.MeetingType, duration time.Duration) {
				// Meeting ended
				ts := time.Now().Format("2006/01/02 15:04:05 MST")
				mins := meetings.RoundToNearestMinute(duration)
				switch meetingType {
				case meetings.MeetingTypeZoom:
					if cfg.Metadata.ZoomDetection {
						if err := multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting ended: %s zoom (duration: %dm)\n", ts, mins)); err != nil {
							logWriteErr("failed to write zoom meeting end metadata", err)
						}
					}
				case meetings.MeetingTypeMeet:
					if cfg.Metadata.MeetDetection {
						if err := multiWriter.WriteMetadata(fmt.Sprintf("%%%% meeting ended: %s meet (duration: %dm)\n", ts, mins)); err != nil {
							logWriteErr("failed to write meet meeting end metadata", err)
						}
					}
				}

				// Execute on_meeting_end plugins
				if pluginRunner.HasTrigger(config.TriggerOnMeetingEnd) {
					execCtx := &plugins.ExecuteContext{
						OutputFile:      outputPath,
						MeetingType:     meetingType,
						MeetingDuration: duration,
					}
					go pluginRunner.Execute(pluginCtx, config.TriggerOnMeetingEnd, execCtx)
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

	// Dead air watchdog
	deadAirCh := make(chan struct{}, 1)
	if cfg.DeadAirReset.Duration() > 0 {
		threshold := cfg.DeadAirReset.Duration()
		interval := time.Second
		if threshold < time.Second {
			interval = threshold / 2
			if interval < 100*time.Millisecond {
				interval = 100 * time.Millisecond
			}
		}

		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					if shouldSkip() {
						continue
					}
					if diag.IsDeadAir(threshold) {
						select {
						case deadAirCh <- struct{}{}:
						default:
						}
					}
				}
			}
		}()
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
					diag.SetPaused(true)
					fmt.Fprintf(stderr, "\n[PAUSED] Press Ctrl+Z to resume\n")
				} else {
					diag.SetPaused(false)
					fmt.Fprintf(stderr, "[RESUMED]\n")
				}
				pauseMu.Unlock()
			}
		}
	}()

	// Goroutine to handle diagnostic dump signal (Ctrl+\)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-diagChan:
				diagPath := "./diagnostic-info.txt"
				if err := diag.DumpToFile(diagPath); err != nil {
					fmt.Fprintf(stderr, "\n[DIAG] Failed to write diagnostics: %v\n", err)
				} else {
					fmt.Fprintf(stderr, "\n[DIAG] Diagnostic info written to %s\n", diagPath)
				}
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
					level := rmsLevel(chunk)
					diag.RecordAudioLevel(level)
					// Skip sending when paused or reconnecting
					if shouldSkip() {
						diag.RecordChunkDropped()
						continue
					}
					if err := wsClient.SendAudio(chunk); err != nil {
						diag.RecordSendError(err)
						if !wsClient.IsClosed() {
							workerErr <- fmt.Errorf("send error: %w", err)
						}
						return
					}
					diag.RecordChunkSent()
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
						diag.RecordRecvError(err)
						if !wsClient.IsClosed() {
							workerErr <- fmt.Errorf("receive error: %w", err)
						}
						return
					}

					switch m := msg.(type) {
					case *client.WordMessage:
						output := postProc.ProcessWord(m.Text)
						diag.RecordWordMessage(m.Text, output != "")
						if output != "" {
							if err := multiWriter.Write(output); err != nil {
								logWriteErr("failed to write transcript output", err)
							}
						}

					case *client.StepMessage:
						isEOT := m.IsEndOfTurn()
						diag.RecordStepMessage(isEOT)
						if isEOT {
							if cfg.Debug {
								fmt.Fprintf(stderr, "[DEBUG] End of turn detected\n")
							}
							output := postProc.ProcessEndOfTurn()
							if output != "" {
								if err := multiWriter.Write(output); err != nil {
									logWriteErr("failed to write transcript output", err)
								}
							}
						}

					case *client.EndWordMessage:
						// EndWord marks word boundary timing - informational only
						diag.RecordEndWordMessage()

					case *client.ReadyMessage:
						// Server is ready to accept audio
						diag.RecordReadyMessage()
						if cfg.Debug {
							fmt.Fprintf(stderr, "[DEBUG] Server ready\n")
						}

					case *client.ErrorMessage:
						// Server reported an error
						diag.RecordErrorMessage(m.Message)
						fmt.Fprintf(stderr, "[SERVER ERROR] %s\n", m.Message)

					case *client.MarkerMessage:
						// Marker sync acknowledgment
						diag.RecordMarkerMessage()

					default:
						// Truly unknown message type
						msgType := ""
						if msg != nil {
							msgType = msg.MessageType()
						}
						diag.RecordUnknownMessage(msgType)
					}
				}
			}
		}()

		return workerDone, workerErr
	}

	workerDone, workerErr := startWorkers()

	reconnect := func(reason string) bool {
		if reason != "" {
			fmt.Fprintf(stderr, "\n%s\n", reason)
		}

		// Set reconnecting state
		reconnectMu.Lock()
		reconnecting = true
		reconnectMu.Unlock()
		diag.SetReconnecting(true)
		diag.SetConnected(false, cfg.ServerURL)

		// Stop current workers
		close(workerDone)

		// Attempt reconnection
		fmt.Fprintf(stderr, "Attempting to reconnect...\n")
		reconnectErr := wsClient.ReconnectContext(reconnectCtx, 0, func(attempt int, delay time.Duration) {
			select {
			case <-stopChan:
				return
			default:
			}
			fmt.Fprintf(stderr, "  Reconnection attempt %d (waiting %v)...\n", attempt, delay)
		})

		if reconnectErr != nil {
			if errors.Is(reconnectErr, context.Canceled) {
				return false
			}
			fmt.Fprintf(stderr, "Reconnection failed: %v\n", reconnectErr)
			return false
		}

		fmt.Fprintf(stderr, "Reconnected successfully.\n")
		diag.SetConnected(true, cfg.ServerURL)
		diag.ResetDeadAirTracking()

		// Clear reconnecting state and restart workers
		reconnectMu.Lock()
		reconnecting = false
		reconnectMu.Unlock()
		diag.SetReconnecting(false)

		workerDone, workerErr = startWorkers()
		return true
	}

	// Main loop with reconnection handling
	for {
		select {
		case <-stopChan:
			fmt.Fprintf(stderr, "\nStopping...\n")
			goto shutdown

		case <-deadAirCh:
			if !reconnect("[WARN] Dead air detected; reconnecting...") {
				goto shutdown
			}

		case err := <-workerErr:
			if !reconnect(fmt.Sprintf("Connection error: %v", err)) {
				goto shutdown
			}
		}
	}

shutdown:
	// Clean shutdown
	close(done)
	close(heartbeatDone)
	pluginRunner.StopPeriodic()
	pluginCancel()
	// Close workerDone safely (it might already be closed during reconnection)
	select {
	case <-workerDone:
		// Already closed
	default:
		close(workerDone)
	}
	if err := capture.Stop(); err != nil {
		logWriteErr("failed to stop capture", err)
	}
	if err := wsClient.Close(); err != nil {
		logWriteErr("failed to close websocket", err)
	}

	// Final newline and flush
	if err := multiWriter.Write("\n"); err != nil {
		logWriteErr("failed to write final newline", err)
	}
	if err := multiWriter.Flush(); err != nil {
		logWriteErr("failed to flush output", err)
	}

	fmt.Fprintf(stderr, "Transcript saved to: %s\n", outputPath)
	return nil
}

func rmsLevel(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, sample := range samples {
		v := float64(sample)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}
