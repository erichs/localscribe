// transcription.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/AssemblyAI/assemblyai-go-sdk"
)

// TranscriptionBackend is an interface for sending audio data to a real-time
// transcription service and handling connect/disconnect.
type TranscriptionBackend interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context, wait bool) error
	Send(ctx context.Context, data []byte) error
	KeepAlive(ctx context.Context) error
}

// AssemblyAIBackend is a concrete backend implementing TranscriptionBackend
// using the AssemblyAI Go SDK.
type AssemblyAIBackend struct {
	client      *assemblyai.RealTimeClient
	transcriber *assemblyai.RealTimeTranscriber
}

// NewAssemblyAIBackend returns an AssemblyAIBackend that implements
// our TranscriptionBackend interface. We pass in an API key, plus callbacks
// for handling transcripts/errors.
func NewAssemblyAIBackend(cfg Config) *AssemblyAIBackend {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		log.Println("Error getting terminal size:", err)
		return nil
	}

	transcriber := &assemblyai.RealTimeTranscriber{
		OnSessionBegins: func(e assemblyai.SessionBegins) {
			log.Println("transcription session start")
		},
		OnSessionTerminated: func(e assemblyai.SessionTerminated) {
			log.Println("transcription session end")
		},
		OnFinalTranscript: func(t assemblyai.FinalTranscript) {
			// Print final transcripts
			fmt.Println(t.Text)
			line := fmt.Sprintf("%s - %s", getDateTime(), t.Text)
			atomicAppendToFile(cfg.LogFile, line)
		},
		OnPartialTranscript: func(t assemblyai.PartialTranscript) {
			maxWidth := width - 2
			var displayText string

			if len(t.Text) > maxWidth {
				// Truncate text to fit the terminal width
				displayText = t.Text[len(t.Text)-maxWidth:]
			} else {
				displayText = t.Text
			}

			// Overwrite the same line with the truncated text
			fmt.Printf("\r%-*s\r", maxWidth, displayText)
		},
		OnError: func(err error) {
			log.Printf("AssemblyAI error: %v\n", err)
		},
	}

	client := assemblyai.NewRealTimeClientWithOptions(
		assemblyai.WithRealTimeAPIKey(cfg.AssemblyAIKey),
		assemblyai.WithRealTimeTranscriber(transcriber),
		assemblyai.WithRealTimeSampleRate(cfg.SampleRate),
	)

	return &AssemblyAIBackend{
		client:      client,
		transcriber: transcriber,
	}
}

// Connect opens the WebSocket connection to AssemblyAI's real-time API.
func (a *AssemblyAIBackend) Connect(ctx context.Context) error {
	return a.client.Connect(ctx)
}

// Disconnect ends the WebSocket connection, optionally waiting for final transcripts.
func (a *AssemblyAIBackend) Disconnect(ctx context.Context, wait bool) error {
	return a.client.Disconnect(ctx, wait)
}

// Send streams audio data to the real-time API.
func (a *AssemblyAIBackend) Send(ctx context.Context, data []byte) error {
	return a.client.Send(ctx, data)
}

func (a *AssemblyAIBackend) KeepAlive(ctx context.Context) error {
	return a.client.ForceEndUtterance(ctx)
}

// StartTranscriptionLoop handles the main microphone read/send loop.
// It assumes the backend is connected and we have a valid recorder.
func StartTranscriptionLoop(
	ctx context.Context,
	backend TranscriptionBackend,
	rec *recorder, // from microphone.go logic
) error {
	if rec == nil {
		return errors.New("no recorder provided")
	}

	// Start capturing audio from the microphone
	if err := rec.Start(); err != nil {
		return fmt.Errorf("recorder start failed: %w", err)
	}
	defer cleanupRecorder(rec)

	// Initialize ticker for keep-alive or other periodic tasks if needed
	// (Optional based on your current implementation)

	for {
		select {
		case <-ctx.Done():
			// Context canceled (e.g., user hit Ctrl-C)
			return nil
		default:
			// Read audio samples from the microphone
			audioData, err := rec.Read()
			if err != nil {
				return fmt.Errorf("read from recorder failed: %w", err)
			}

			// If paused, skip sending
			if paused {
				backend.KeepAlive(ctx)
				time.Sleep(50 * time.Millisecond)
				continue
			}

			// Attempt to send data to the backend
			err = backend.Send(ctx, audioData)
			if err != nil {
				log.Printf("Warning: failed to send data to backend: %v", err)

				// Handle backend unavailability
				// Attempt to reconnect with exponential backoff
				if err := handleBackendReconnect(ctx, backend); err != nil {
					log.Printf("Error during backend reconnection: %v", err)
					// Depending on your preference, you can choose to continue or return
					// Here, we'll continue to allow further retries
					continue
				}

				// After reconnection, continue the loop to retry sending
				continue
			}
		}
	}
}

// handleBackendReconnect attempts to reconnect the backend with exponential backoff.
// It logs each attempt and respects the context cancellation.
func handleBackendReconnect(ctx context.Context, backend TranscriptionBackend) error {
	backoff := time.Second * 5     // Initial backoff duration
	maxBackoff := time.Second * 30 // Maximum backoff duration

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled during reconnection")
		default:
			log.Println("Attempting to reconnect to transcription backend...")

			// Attempt to disconnect gracefully before reconnecting
			if err := backend.Disconnect(ctx, false); err != nil {
				log.Printf("Warning: failed to disconnect backend: %v", err)
			}

			// Attempt to reconnect
			err := backend.Connect(ctx)
			if err != nil {
				log.Printf("Reconnect attempt failed: %v", err)
				log.Printf("Retrying in %v...", backoff)
				time.Sleep(backoff)

				// Exponential backoff with a cap
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}

			log.Println("Successfully reconnected to transcription backend.")
			return nil
		}
	}
}

// getDateTime returns the current local time formatted as specified.
func getDateTime() string {
	return time.Now().Format("2006/01/02 15:04:05 EST")
}
