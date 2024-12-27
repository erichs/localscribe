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

var paused bool // Global or shared var to track pause state

func main() {
    var (
        apiKey          string
        sampleRate      int
        framesPerBuffer int
    )

    flag.StringVar(&apiKey, "apiKey", "", "AssemblyAI API key")
    flag.IntVar(&sampleRate, "sampleRate", 16000, "Audio sample rate (Hz)")
    flag.IntVar(&framesPerBuffer, "framesPerBuffer", 3200,
        "Number of frames to process per buffer")
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

    // Our chosen backend
    backend := NewAssemblyAIBackend(apiKey, sampleRate)

    // Set up the context for cancellation
    ctx, cancel := context.WithCancel(context.Background())

    // Set up signal handling for both SIGINT (Ctrl-C) and SIGTSTP (Ctrl-Z)
    go func() {
        sigCh := make(chan os.Signal, 1)
        // Listen for Ctrl-C (SIGINT) and Ctrl-Z (SIGTSTP), plus SIGTERM if you want
        signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGTSTP)
        for {
            s := <-sigCh
            switch s {
            case os.Interrupt, syscall.SIGTERM:
                // The user wants to exit. Pause sending so we don't get session-closed errors.
                paused = true
                fmt.Println("\nlocalscribe: caught shutdown signal...")
                // Disconnect from backend
                if err := backend.Disconnect(ctx, true); err != nil {
                    log.Printf("warning: backend disconnect error: %v\n", err)
                }
                cancel()
                return

            case syscall.SIGTSTP:
                // Toggle paused
                paused = !paused
                if paused {
                    fmt.Print("PAUSED\r")
                } else {
                    // Clear out the text
                    fmt.Print("      \r")
                }
            }
        }
    }()

    fmt.Println("Connecting to transcription backend...")
    if err := backend.Connect(ctx); err != nil {
        log.Fatalf("connect to backend failed: %v\n", err)
    }

    fmt.Println("Beginning transcription loop. Press Ctrl+Z to pause/resume, Ctrl+C to quit.")

    if err := StartTranscriptionLoop(ctx, backend, rec); err != nil {
        log.Printf("transcription loop ended: %v\n", err)
    }

    fmt.Println("localscribe: exiting gracefully.")
}
