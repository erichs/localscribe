package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gordonklaus/portaudio"
)

var paused bool // Global or shared var to track pause state
type Config struct {
	LogFile         string
	OpenAIAPIKey    string
	SampleRate      int
	FramesPerBuffer int
}

var (
	defaultNowString = time.Now().Format("20060102_1504")
	defaultLogFile   = "transcription-" + defaultNowString + ".log"
)

func parseFlags() Config {
	logFileEnv := os.Getenv("TRANSCRIPTION_FILE")
	openAIKeyEnv := os.Getenv("OPENAI_API_KEY")

	cfg := Config{
		LogFile:      defaultLogFile,
		OpenAIAPIKey: openAIKeyEnv,
	}

	flag.IntVar(&cfg.SampleRate, "sampleRate", 16000, "Audio sample rate (Hz)")
	flag.IntVar(&cfg.FramesPerBuffer, "framesPerBuffer", 3200,
		"Number of frames to process per buffer")
	flag.StringVar(&cfg.LogFile, "l", strWithFallback(logFileEnv, cfg.LogFile),
		"Path to the transcription log file (default: TRANSCRIPTION_FILE or time-based).")
	flag.StringVar(&cfg.OpenAIAPIKey, "k", cfg.OpenAIAPIKey,
		"OpenAI API key (default: OPENAI_API_KEY).")

	flag.Parse()

	return cfg
}

// strWithFallback returns str if not empty, otherwise fallback.
func strWithFallback(str, fallback string) string {
	if str != "" {
		return str
	}
	return fallback
}

func main() {

	cfg := parseFlags()

	if cfg.OpenAIAPIKey == "" {
		fmt.Fprintln(os.Stderr, "Error: Must provide -k <APIKEY> or set OPENAI_API_KEY env var")
		os.Exit(1)
	}

	fmt.Println("localscribe: initializing PortAudio...")
	if err := portaudio.Initialize(); err != nil {
		log.Fatalf("portaudio init failed: %v\n", err)
	}
	defer portaudio.Terminate()

	fmt.Printf("Creating new recorder: sampleRate=%d, framesPerBuffer=%d\n",
		cfg.SampleRate, cfg.FramesPerBuffer)
	rec, err := newRecorder(cfg.SampleRate, cfg.FramesPerBuffer)
	if err != nil {
		log.Fatalf("failed to create recorder: %v\n", err)
	}

	backend := NewAssemblyAIBackend(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	fmt.Printf("Transcribing to file: %s\n", cfg.LogFile)

	go func() {
		sigCh := make(chan os.Signal, 1)
		// Listen for Ctrl-C (SIGINT) and Ctrl-Z (SIGTSTP), plus SIGTERM
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGTSTP)
		for {
			s := <-sigCh
			switch s {
			case os.Interrupt, syscall.SIGTERM:
				// Pause sending so we don't get session-closed errors.
				paused = true
				fmt.Println("\nlocalscribe: caught shutdown signal...")
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

	// heartbeat appends a timestamp line to the log file every minute, until ctx is done.
	go heartbeat(ctx, cfg)

	go pollZoomStatus(ctx, cfg)

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

// atomicAppendToFile appends text to a file, creating it if necessary.
// it uses a simple semaphore locking mechanism to limit concurrency (block)
var sem = make(chan int, 1)

func atomicAppendToFile(path, text string) error {
	sem <- 1 // acquire lock
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text + "\n")
	<-sem // release lock
	return err
}
