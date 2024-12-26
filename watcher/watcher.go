package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Command format: ### last N | <pattern>
// Heartbeat format: %%% heartbeat 2024-12-24T13:00:01Z
// Normal transcript lines: anything else (no special prefix).

// We'll keep it simple: read new lines from the file, detect commands,
// and spawn goroutines that do "last N" scanning, then call
// "fabric --pattern <pattern>".

// Config holds CLI flags.
type Config struct {
	LogFile        string
	PersistOutFile string
}

// Command represents something like "last 5 | summarize"
type Command struct {
	Minutes int
	Pattern string
}

func main() {
	cfg := parseFlags()

	// Setup graceful shutdown via context cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Listen for signals (Ctrl-C, SIGTERM).
	go handleSignals(cancel)

	fmt.Printf("Starting watcher on file: %s\n", cfg.LogFile)
	fmt.Println("Press Ctrl-C to exit...")

	if err := watchLoop(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "watchLoop error: %v\n", err)
		os.Exit(1)
	}
}

// parseFlags sets up command-line flags.
func parseFlags() Config {
	var cfg Config
	flag.StringVar(&cfg.LogFile, "logFile", "transcription.log",
		"Path to the log file to watch (defaults to 'transcription.log').")
	flag.StringVar(&cfg.PersistOutFile, "persistOut", "",
		"Optional path to a file for appending command outputs.")
	flag.Parse()
	return cfg
}

// handleSignals listens for INT/TERM and cancels the context.
func handleSignals(cancel context.CancelFunc) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	fmt.Println("Received signal, shutting down watcher...")
	cancel()
}

// watchLoop repeatedly reads from the end of cfg.LogFile for new lines.
// Each time we find a line starting with "###", we parse it as a command
// and spawn a goroutine to handle it.
func watchLoop(ctx context.Context, cfg Config) error {
	// Open (or create) the file.
	f, err := os.Open(cfg.LogFile)
	if err != nil {
		// If file doesn’t exist, try creating it first
		if os.IsNotExist(err) {
			_, _ = os.Create(cfg.LogFile)
			f, err = os.Open(cfg.LogFile)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	defer f.Close()

	// Seek to end so we don't process old lines on start
	if _, err = f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek error: %w", err)
	}

	reader := bufio.NewReader(f)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// Try to read a new line.
			line, errRead := reader.ReadString('\n')
			if errRead != nil {
				if errRead == io.EOF {
					// No new line available; sleep a bit and retry.
					time.Sleep(1 * time.Second)
					continue
				}
				return fmt.Errorf("read error: %w", errRead)
			}
			// line includes the trailing '\n'. We'll trim it:
			line = strings.TrimRight(line, "\r\n")

			// Check for command lines
			if strings.HasPrefix(line, "###") {
				cmdStr := strings.TrimPrefix(line, "###")
				cmdStr = strings.TrimSpace(cmdStr)
				cmd, parseErr := parseCommand(cmdStr)
				if parseErr != nil {
					fmt.Fprintf(os.Stderr, "Could not parse command '%s': %v\n", cmdStr, parseErr)
					continue
				}
				go handleCommand(cmd, cfg) // concurrency: each command in a goroutine
			}
			// Otherwise, we ignore normal transcript/metadata lines
		}
	}
}

// parseCommand expects something like "last 5 | summarize"
func parseCommand(cmdStr string) (Command, error) {
	// Example: "last 10 | summarize"
	// We'll split on '|'
	parts := strings.Split(cmdStr, "|")
	if len(parts) != 2 {
		return Command{}, fmt.Errorf("invalid format, expected 'last N | pattern'")
	}
	left := strings.TrimSpace(parts[0])  // e.g. "last 10"
	right := strings.TrimSpace(parts[1]) // e.g. "summarize"

	// left should be "last N"
	if !strings.HasPrefix(left, "last ") {
		return Command{}, fmt.Errorf("invalid left side '%s', expected 'last N'", left)
	}
	numStr := strings.TrimPrefix(left, "last ")
	numStr = strings.TrimSpace(numStr) // "10"
	minutes, err := strconv.Atoi(numStr)
	if err != nil {
		return Command{}, fmt.Errorf("parsing minutes: %w", err)
	}

	return Command{Minutes: minutes, Pattern: right}, nil
}

// handleCommand does the "last N minutes" scanning plus calls fabric.
func handleCommand(cmd Command, cfg Config) {
	fmt.Printf("Handling command: last %d minutes | %s\n", cmd.Minutes, cmd.Pattern)

	// 1. Gather lines from last N minutes (approx) by scanning backward.
	lines, err := gatherLines(cmd.Minutes, cfg.LogFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error gathering lines for command: %v\n", err)
		return
	}

	// 2. Filter out lines that start with "###" or "%%%".
	cleaned := filterUnwanted(lines)

	// 3. Call fabric, piping in `cleaned`.
	out, err := runFabric(cleaned, cmd.Pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fabric error: %v\n", err)
		return
	}

	// 4. Print result to stdout
	fmt.Println("Fabric Output:")
	fmt.Println(out)

	// 5. Optionally append to persist file
	if cfg.PersistOutFile != "" {
		appendErr := appendToFile(cfg.PersistOutFile, out)
		if appendErr != nil {
			fmt.Fprintf(os.Stderr, "Could not persist output: %v\n", appendErr)
		}
	}
}

// gatherLines scans backward from the end of logFile, looking for a heartbeat
// line that's >= N minutes old, and returns all lines from there to the end.
//
// Because transcript lines lack timestamps, we rely on lines that look like:
//   %%% heartbeat 2024-12-24T13:00:01Z
// If we can't find such a line, we'll just return everything (worst-case).
func gatherLines(nMinutes int, logFile string) ([]string, error) {
	f, err := os.Open(logFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := fi.Size()
	var chunkSize int64 = 4096
	offset := fileSize

	cutoff := time.Now().Add(-time.Duration(nMinutes) * time.Minute)
	var foundPos int64 = 0

	scannerBuf := make([]byte, 0, 8192)
	for offset > 0 {
		readSize := chunkSize
		if offset < chunkSize {
			readSize = offset
		}
		offset -= readSize

		buf := make([]byte, readSize)
		if _, err := f.ReadAt(buf, offset); err != nil {
			return nil, err
		}

		// We'll prepend this chunk to scannerBuf
		scannerBuf = append(buf, scannerBuf...)

		// Attempt to parse lines from scannerBuf by splitting on '\n'
		lines := bytes.Split(scannerBuf, []byte("\n"))

		// We'll check from the end to the start for a heartbeat line
		for i := len(lines) - 1; i >= 0; i-- {
			l := lines[i]
			if bytes.HasPrefix(l, []byte("%%% heartbeat ")) {
				strTime := bytes.TrimPrefix(l, []byte("%%% heartbeat "))
				parsed, errTime := time.Parse(time.RFC3339, string(strTime))
				if errTime != nil {
					continue
				}
				if parsed.Before(cutoff) || parsed.Equal(cutoff) {
					// found a heartbeat older than cutoff
					// read from line i+1 to the end
					pos, errPos := findLineStartPos(f, offset, lines[:i+1])
					if errPos == nil {
						foundPos = pos
						goto doneBackward
					}
				}
			}
		}

		// if offset == 0, we scanned entire file
		if offset == 0 {
			// no older heartbeat found, read from beginning
			foundPos = 0
		}
	}
doneBackward:

	// now read everything from foundPos to the end
	if _, err := f.Seek(foundPos, io.SeekStart); err != nil {
		return nil, err
	}
	var result []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		result = append(result, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// findLineStartPos calculates the file offset corresponding to the line
// immediately after linesSeen (which is lines up to i+1).
func findLineStartPos(f *os.File, offset int64, linesSeen [][]byte) (int64, error) {
	var totalBytes int
	for i := range linesSeen {
		// +1 for newline
		totalBytes += len(linesSeen[i]) + 1
	}
	return offset + int64(totalBytes), nil
}

// filterUnwanted removes lines that start with "%%%" or "###".
func filterUnwanted(lines []string) []string {
	var out []string
	for _, ln := range lines {
		if strings.HasPrefix(ln, "%%%") {
			continue
		}
		if strings.HasPrefix(ln, "###") {
			continue
		}
		out = append(out, ln)
	}
	return out
}

// runFabric pipes `textLines` to `fabric --pattern cmdPattern`.
func runFabric(textLines []string, cmdPattern string) (string, error) {
	cmdArgs := []string{"--pattern", cmdPattern}
	fabricCmd := exec.Command("fabric", cmdArgs...)

	stdin, err := fabricCmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}

	go func() {
		defer stdin.Close()
		for _, line := range textLines {
			io.WriteString(stdin, line+"\n")
		}
	}()

	output, err := fabricCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("fabric command error: %w (output: %s)", err, string(output))
	}
	return string(output), nil
}

// appendToFile appends text to a file, creating it if necessary.
func appendToFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text + "\n")
	return err
}
