package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/AssemblyAI/assemblyai-go-sdk"
)

// TranscriptionBackend is an interface for sending audio data to a real-time
// transcription service and handling connect/disconnect.
type TranscriptionBackend interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context, wait bool) error
	Send(ctx context.Context, data []byte) error
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
func NewAssemblyAIBackend(apiKey string, sampleRate int) *AssemblyAIBackend {
	transcriber := &assemblyai.RealTimeTranscriber{
		OnSessionBegins: func(e assemblyai.SessionBegins) {
			fmt.Println("session begins")
		},
		OnSessionTerminated: func(e assemblyai.SessionTerminated) {
			fmt.Println("session terminated")
		},
		OnFinalTranscript: func(t assemblyai.FinalTranscript) {
			// Print final transcripts
			fmt.Println(t.Text)
		},
		OnPartialTranscript: func(t assemblyai.PartialTranscript) {
			// Overwrite partial transcript on same line
			fmt.Printf("\r%s", t.Text)
		},
		OnError: func(err error) {
			log.Printf("AssemblyAI error: %v\n", err)
		},
	}

	client := assemblyai.NewRealTimeClientWithOptions(
		assemblyai.WithRealTimeAPIKey(apiKey),
		assemblyai.WithRealTimeTranscriber(transcriber),
		assemblyai.WithRealTimeSampleRate(sampleRate),
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

// StartTranscriptionLoop handles the main microphone read/send loop.
// It assumes the backend is connected and we have a valid recorder.
func StartTranscriptionLoop(
	ctx context.Context,
	backend TranscriptionBackend,
	rec *recorder, // from recorder.go logic
) error {
	if rec == nil {
		return errors.New("no recorder provided")
	}
	defer rec.Close()

	// Start capturing audio from the microphone
	if err := rec.Start(); err != nil {
		return fmt.Errorf("recorder start failed: %w", err)
	}
	defer rec.Stop()

	for {
		select {
		case <-ctx.Done():
			// context canceled (e.g., user hit Ctrl-C)
			return nil
		default:
			// Read audio samples from the microphone
			audioData, err := rec.Read()
			if err != nil {
				return fmt.Errorf("read from recorder failed: %w", err)
			}

			// Send partial samples to the transcription backend
			if err := backend.Send(ctx, audioData); err != nil {
				return fmt.Errorf("send to backend failed: %w", err)
			}
		}
	}
}
