package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/gordonklaus/portaudio"
)

var paused bool // Global or shared var to track pause state
type Config struct {
	LogFile         string
	AssemblyAIKey   string
	SampleRate      int
	FramesPerBuffer int
	Context         context.Context
	RESTPort        int
}

var (
	defaultNowString = time.Now().Format("20060102_1504")
	defaultLogFile   = path.Join(os.Getenv("HOME"), ".local", "scribe", "transcription-"+defaultNowString+".log")
)

func initConfig(ctx context.Context) Config {
	logFileEnv := os.Getenv("TRANSCRIPTION_FILE")
	assemblyAIKey := os.Getenv("ASSEMBLYAI_API_KEY")

	cfg := Config{
		LogFile:       defaultLogFile,
		AssemblyAIKey: assemblyAIKey,
		Context:       ctx,
	}

	flag.IntVar(&cfg.RESTPort, "restPort", 8080, "Port to listen on for REST queries")
	flag.IntVar(&cfg.SampleRate, "sampleRate", 16000, "Audio sample rate (Hz)")
	flag.IntVar(&cfg.FramesPerBuffer, "framesPerBuffer", 3200,
		"Number of frames to process per buffer")
	flag.StringVar(&cfg.LogFile, "l", strWithFallback(logFileEnv, cfg.LogFile),
		"Path to the transcription log file (default: TRANSCRIPTION_FILE or time-based).")
	flag.StringVar(&cfg.AssemblyAIKey, "k", cfg.AssemblyAIKey,
		"OpenAI API key (default: OPENAI_API_KEY).")

	flag.Parse()

	if cfg.AssemblyAIKey == "" {
		fmt.Fprintln(os.Stderr, "Error: Must provide -k <APIKEY> or set OPENAI_API_KEY env var")
		os.Exit(1)
	}

	return cfg
}

// strWithFallback returns str if not empty, otherwise fallback.
func strWithFallback(str, fallback string) string {
	if str != "" {
		return str
	}
	return fallback
}

func initPortAudio() {
	log.Println("initializing PortAudio")
	if err := portaudio.Initialize(); err != nil {
		log.Fatalf("portaudio init failed: %v\n", err)
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := initConfig(ctx)

	initPortAudio()
	defer portaudio.Terminate()

	log.Printf("starting voice recorder: sampleRate=%d, framesPerBuffer=%d\n",
		cfg.SampleRate, cfg.FramesPerBuffer)
	rec, err := newRecorder(cfg.SampleRate, cfg.FramesPerBuffer)
	if err != nil {
		log.Fatalf("failed to create recorder: %v\n", err)
	}

	backend := NewAssemblyAIBackend(cfg)

	log.Printf("transcribing to file: %s\n", cfg.LogFile)

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
				log.Println("\r\nlocalscribe: shutdown received...")
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

	go pollZoomStatus(cfg)
	go launchRESTServer(cfg)
	go pollChromeHistory(cfg)

	scribeIPInfo(cfg)

	log.Println("connecting to transcription backend")
	if err := backend.Connect(ctx); err != nil {
		log.Fatalf("connect to backend failed: %v\n", err)
	}

	log.Println("Press Ctrl+Z to pause/resume, Ctrl+C to quit.")

	if err := StartTranscriptionLoop(ctx, backend, rec); err != nil {
		log.Fatalf("transcription loop error: %v\n", err)
	}

	log.Println("localscribe exit: OK")
}

func launchRESTServer(cfg Config) {
	if err := startRESTServer(cfg); err != nil {
		log.Printf("web server exited with error: %v\n", err)
	}
}

// atomicAppendToFile appends text to a file, creating it if necessary.
// it uses a simple semaphore locking mechanism to limit concurrency (block)
var lockSem = make(chan int, 1)

func atomicAppendToFile(path, text string) error {
	lockSem <- 1 // acquire lock
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text + "\n")
	<-lockSem // release lock
	return err
}

func scribeIPInfo(cfg Config) {
	info := fetchIPInfo()
	line := fmt.Sprintf("%s %s - %s\n", getDateTime(), "%%% ipinfo", info)
	atomicAppendToFile(cfg.LogFile, line)
}

func getDateTime() string {
	return time.Now().Format("2006/01/02 15:04:05 EST")
}
