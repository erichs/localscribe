package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gordonklaus/portaudio"
)

// localscribe is the top-level CLI that:
// 1) Parses flags (API key, sample rate, frames per buffer).
// 2) Initializes the chosen TranscriptionBackend (AssemblyAI).
// 3) Connects to the backend.
// 4) Sets up PortAudio + recorder.
// 5) Runs the transcription loop until Ctrl-C / SIGTERM.

func main() {
	var (
		apiKey         string
		sampleRate     int
		framesPerBuffer int
	)

	flag.StringVar(&apiKey, "apiKey", "", "AssemblyAI API key")
	flag.IntVar(&sampleRate, "sampleRate", 16000, "Audio sample rate (Hz)")
	flag.IntVar(&framesPerBuffer, "framesPerBuffer", 3200,
		"Number of frames to process per buffer (e.g., 3200 => ~200ms at 16kHz)")

	flag.Parse()

	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: Must provide -apiKey")
		os.Exit(1)
	}

	fmt.Println("localscribe: initializing PortAudio...")
	if err := portaudio.Initialize(); err != nil {
		log.Fatalf("portaudio init failed: %v\n", err)
	}
	defer portaudio.Terminate()

	fmt.Printf("Creating new recorder: sampleRate=%d, framesPerBuffer=%d\n",
		sampleRate, framesPerBuffer)
	rec, err := newRecorder(sampleRate, framesPerBuffer)
	if err != nil {
		log.Fatalf("failed to create recorder: %v\n", err)
	}

	// In the future, you could swap out to a different backend if needed:
	backend := NewAssemblyAIBackend(apiKey, sampleRate)

	ctx, cancel := context.WithCancel(context.Background())

	// Set up signal handling so we can gracefully exit on Ctrl-C
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nlocalscribe: caught shutdown signal, disconnecting...")

		// Attempt a graceful teardown
		if err := backend.Disconnect(ctx, true); err != nil {
			log.Printf("warning: backend disconnect error: %v\n", err)
		}

		cancel()
	}()

	fmt.Println("Connecting to transcription backend...")
	if err := backend.Connect(ctx); err != nil {
		log.Fatalf("connect to backend failed: %v\n", err)
	}

	fmt.Println("Beginning transcription loop. Speak into the microphone...")
	if err := StartTranscriptionLoop(ctx, backend, rec); err != nil {
		log.Printf("transcription loop ended with error: %v\n", err)
	}

	fmt.Println("localscribe: exiting gracefully.")
}
