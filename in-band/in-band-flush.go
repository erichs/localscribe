package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Global defaults (can be overridden via flags or environment variables).
var (
	defaultNowString   = time.Now().Format("20060102_1504")
	defaultLogFile     = "transcription-" + defaultNowString + ".log"
	defaultOffsetFile  = "/tmp/transcription_offset-" + defaultNowString
	defaultSystemPrompt = `You are a helpful AI agent responding to transcribed audio
from work meetings. Analyze the text shown, and assume that it is part of
a conversation in progress. Extract the salient points and provide a terse,
concise summary of helpful replies, always from a cybersecurity and engineering
perspective. Limit output to one sentence per unique thought. If your output
contains more than one thought, emit them as a bullet-point list.`
)

// Config holds configuration values for the program.
type Config struct {
	LogFile       string
	OffsetFile    string
	SystemPrompt  string
	OpenAIAPIKey  string
}

// OpenAIRequest is the JSON payload for Chat Completion requests.
type OpenAIRequest struct {
	Model    string           `json:"model"`
	Messages []OpenAIChatMsg  `json:"messages"`
}

// OpenAIChatMsg is a single message in a chat prompt.
type OpenAIChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIResponse represents a subset of the JSON response from OpenAI.
type OpenAIResponse struct {
	Error   *OpenAIError       `json:"error,omitempty"`
	Choices []OpenAIChoice     `json:"choices,omitempty"`
}

// OpenAIError is the shape of an error payload from OpenAI.
type OpenAIError struct {
	Message string `json:"message"`
}

// OpenAIChoice is part of the response that includes the assistant’s message.
type OpenAIChoice struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// main sets up configuration, starts heartbeat, starts reading loop, and handles signals.
func main() {
	cfg := parseFlags()

	// Validate required configs
	if cfg.OpenAIAPIKey == "" {
		fmt.Fprintln(os.Stderr, "Error: OPENAI_API_KEY is not set. Use -k or env var OPENAI_API_KEY.")
		os.Exit(2)
	}

	// Create a context that cancels on Ctrl-C / SIGTERM.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Catch interrupts to stop cleanly.
	go handleSignals(cancel)

	// Start heartbeat in a separate goroutine.
	go heartbeat(ctx, cfg.LogFile)

	// Run main loop (blocking).
	if err := runMainLoop(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
}

// parseFlags builds a Config by combining environment vars and flags.
func parseFlags() Config {
	logFileEnv := os.Getenv("TRANSCRIPTION_FILE")
	offsetFileEnv := os.Getenv("OFFSET_FILE")
	systemPromptEnv := os.Getenv("SYSTEM_PROMPT")
	openAIKeyEnv := os.Getenv("OPENAI_API_KEY")

	cfg := Config{
		LogFile:       defaultLogFile,
		OffsetFile:    defaultOffsetFile,
		SystemPrompt:  defaultSystemPrompt,
		OpenAIAPIKey:  openAIKeyEnv,
	}

	flag.StringVar(&cfg.LogFile, "l", pickStr(logFileEnv, cfg.LogFile),
		"Path to the transcription log file (default: TRANSCRIPTION_FILE or time-based).")
	flag.StringVar(&cfg.OffsetFile, "o", pickStr(offsetFileEnv, cfg.OffsetFile),
		"Path to offset file (default: OFFSET_FILE or time-based).")
	flag.StringVar(&cfg.SystemPrompt, "p", pickStr(systemPromptEnv, cfg.SystemPrompt),
		"System prompt to send to OpenAI (default: SYSTEM_PROMPT or a helpful AI message).")
	flag.StringVar(&cfg.OpenAIAPIKey, "k", cfg.OpenAIAPIKey,
		"OpenAI API key (default: OPENAI_API_KEY).")

	flag.Parse()

	return cfg
}

// pickStr returns envVal if not empty, otherwise fallback.
func pickStr(envVal, fallback string) string {
	if envVal != "" {
		return envVal
	}
	return fallback
}

// handleSignals listens for interrupt/term signals and cancels the context.
func handleSignals(cancel context.CancelFunc) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	fmt.Println("Received signal, shutting down...")
	cancel()
}

// heartbeat appends a timestamp line to the log file every minute, until ctx is done.
func heartbeat(ctx context.Context, logFile string) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				timestamp := time.Now().String()
				_, _ = f.WriteString(timestamp + "\n")
				f.Close()
			}
		}
	}
}

// runMainLoop continuously reads new lines from the log file, looking for ###FLUSH###.
func runMainLoop(ctx context.Context, cfg Config) error {
	// Get initial offset.
	offset, err := readOffset(cfg.OffsetFile)
	if err != nil {
		return fmt.Errorf("failed to read offset: %w", err)
	}
	fmt.Printf("Starting offset: %d\n", offset)

	// The line buffer will accumulate partial lines if needed.
	var lineBuffer strings.Builder

	// The user transcript accumulates until we flush on ###FLUSH###.
	var transcriptBuffer strings.Builder

	// Open the file once for reading. We'll seek manually.
	file, err := os.Open(cfg.LogFile)
	if err != nil {
		// If file doesn’t exist yet, let's create it so we can tail from empty.
		if errors.Is(err, os.ErrNotExist) {
			_, _ = os.Create(cfg.LogFile)
			file, err = os.Open(cfg.LogFile)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	defer file.Close()

	// Seek to current offset to avoid re-reading old lines.
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	// We’ll read new data in small chunks every second.
	buf := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// Read whatever is available; non-blocking since we don't set any special flags.
			n, readErr := file.Read(buf)
			if n > 0 {
				// Update offset by n bytes read.
				offset += int64(n)
				writeOffset(cfg.OffsetFile, offset)

				// Break into lines.
				chunk := buf[:n]
				lineBuffer.Write(chunk)

				for {
					line, errLine := lineBufferReadLine(&lineBuffer)
					if errLine == io.EOF {
						// No complete line found yet; we’ll keep partial data in lineBuffer.
						break
					}
					// We got a complete line.
					processLine(line, &transcriptBuffer, cfg)
				}
			}

			if readErr != nil && readErr != io.EOF {
				// If it's an actual error (not just EOF with no new data),
				// we can log or return it.
				fmt.Fprintf(os.Stderr, "Error reading file: %v\n", readErr)
			}

			// Sleep a bit to avoid a tight loop.
			time.Sleep(1 * time.Second)
		}
	}
}

// processLine checks if it’s ###FLUSH### or a normal line.
func processLine(line string, transcriptBuffer *strings.Builder, cfg Config) {
	if line == "###FLUSH###" {
		err := flushToOpenAI(transcriptBuffer.String(), cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Flush error: %v\n", err)
		}
		// Clear the transcript buffer either way after flush.
		transcriptBuffer.Reset()
	} else {
		// Accumulate line into transcript buffer, with a newline.
		transcriptBuffer.WriteString(line)
		transcriptBuffer.WriteString("\n")
	}
}

// lineBufferReadLine tries to extract one complete line (delimited by '\n')
// from lineBuffer. If no newline is found, returns io.EOF.
func lineBufferReadLine(lineBuffer *strings.Builder) (string, error) {
	data := lineBuffer.String()
	idx := strings.IndexByte(data, '\n')
	if idx == -1 {
		return "", io.EOF // no full line yet
	}

	// Extract [0..idx], remove from the buffer
	line := data[:idx]
	rest := data[idx+1:]
	lineBuffer.Reset()
	lineBuffer.WriteString(rest)

	return line, nil
}

// flushToOpenAI calls the Chat Completion API with the given transcript text.
func flushToOpenAI(userText string, cfg Config) error {
	payload := OpenAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []OpenAIChatMsg{
			{Role: "system", Content: cfg.SystemPrompt},
			{Role: "user",   Content: userText},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.OpenAIAPIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try to parse an error message.
		var errResp OpenAIResponse
		dec := json.NewDecoder(resp.Body)
		if decodeErr := dec.Decode(&errResp); decodeErr == nil && errResp.Error != nil {
			return fmt.Errorf("openai error: %s (HTTP %d)", errResp.Error.Message, resp.StatusCode)
		}
		return fmt.Errorf("non-200 status: %v", resp.StatusCode)
	}

	var openAIResp OpenAIResponse
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&openAIResp); err != nil {
		return fmt.Errorf("decoding success response: %w", err)
	}

	if len(openAIResp.Choices) > 0 {
		assistantContent := openAIResp.Choices[0].Message.Content
		fmt.Println("AI Response:\n" + assistantContent)
	} else {
		fmt.Println("AI Response: (no content)")
	}
	return nil
}

// readOffset loads the offset from the offset file, or returns 0 if file not found/invalid.
func readOffset(offsetFile string) (int64, error) {
	f, err := os.Open(offsetFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, nil
	}
	line := scanner.Text()
	val, err := strconv.ParseInt(line, 10, 64)
	if err != nil {
		return 0, nil
	}
	return val, nil
}

// writeOffset writes the current offset to the offset file (overwriting old value).
func writeOffset(offsetFile string, off int64) {
	f, err := os.OpenFile(offsetFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		fmt.Fprintf(f, "%d\n", off)
		f.Close()
	}
}
