package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"math"
	"os/exec"
	"strings"
	"time"
)

// ZoomState represents the current state of Zoom.
type ZoomState int

const (
	Unknown ZoomState = iota
	ActiveNoMeeting
	ActiveInMeeting
)

// Previous state
var previousState ZoomState = Unknown

// pollZoomStatus monitors `lsof` output for state transitions.
func pollZoomStatus(cfg Config) {
	log.Println("zoom status polling via lsof, start")
	defer log.Println("zoom status polling end")

	// Set up the lsof command in repeat mode
	cmd := exec.CommandContext(cfg.Context, "lsof", "-i", "4UDP", "-r", "5")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Error creating stdout pipe for lsof: %v", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		log.Fatalf("Error starting lsof: %v", err)
	}

	// Goroutine to handle lsof output
	go func() {
		defer cmd.Wait()
		scanner := bufio.NewScanner(stdout)
		var zoomLines []string

		for scanner.Scan() {
			line := scanner.Text()
			if line == "=======" {
				// Process batch of output when delimiter is encountered
				currentState := determineZoomState(zoomLines)
				if currentState != previousState {
					handleStateTransition(previousState, currentState, cfg)
					previousState = currentState
				}
				zoomLines = nil // Reset for next batch
			} else {
				zoomLines = append(zoomLines, line)
			}
		}

		if err := scanner.Err(); err != nil {
			log.Printf("Error reading lsof output: %v", err)
		}
	}()

	// Wait for context cancellation to stop lsof
	<-cfg.Context.Done()
	log.Println("stopping lsof monitoring...")
	cmd.Process.Kill()
}

// determineZoomState determines the current Zoom state based on lsof output.
func determineZoomState(lines []string) ZoomState {
	zoomLines := filterLines(lines, "zoom.us")
	zoomCount := len(zoomLines)
	switch {
	case zoomCount == 1:
		return ActiveNoMeeting
	case zoomCount >= 2:
		return ActiveInMeeting
	default:
		return Unknown
	}
}

// filterLines filters lines containing a specific substring.
func filterLines(lines []string, substring string) []string {
	var result []string
	for _, line := range lines {
		if strings.Contains(line, substring) {
			result = append(result, line)
		}
	}
	return result
}

var meetingStartTime = time.Now()

// handleStateTransition performs actions based on state transitions.
func handleStateTransition(previous, current ZoomState, cfg Config) {
	switch current {
	case ActiveNoMeeting:
		if previous == Unknown {
			return // ignore initial transition on startup
		}
		meetingDuration := time.Since(meetingStartTime)
		line := fmt.Sprintf("%s %s - %s\n", time.Now().Format("2006/01/02 15:04:05"), "%%% meeting ended", meetingDuration)
		atomicAppendToFile(cfg.LogFile, line)
		// auto-summarize meeting via inline ### command
		// TODO: make this configurable
		n := RoundToNearestMinute(meetingDuration)
		line = fmt.Sprintf("%s %s %d %s\n", time.Now().Format("2006/01/02 15:04:05"), "### last", n, "| summarize")
		atomicAppendToFile(cfg.LogFile, line)
	case ActiveInMeeting:
		meetingStartTime = time.Now()
		meetingUrl, _ := getMeetingURL()
		line := fmt.Sprintf("%s %s - %s\n", time.Now().Format("2006/01/02 15:04:05"), "%%% meeting started", meetingUrl)
		atomicAppendToFile(cfg.LogFile, line)
		meetingTitle, _ := getMeetingTitle()
		if meetingTitle != "" {
			line = fmt.Sprintf("%s %s - %s\n", time.Now().Format("2006/01/02 15:04:05"), "%%% meeting title", meetingTitle)
			atomicAppendToFile(cfg.LogFile, line)
		}
	}
}

func getMeetingURL() (string, error) {
	appleScript := `
if application "zoom.us" is running then
	tell application "zoom.us" to activate
	tell application "System Events"
		tell process "zoom.us"
			keystroke "i" using {command down, shift down}
		end tell
	end tell
end if
`
	cmd := exec.Command("osascript", "-e", appleScript)
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	// Execute pbpaste to retrieve the clipboard content
	pbpasteCmd := exec.Command("pbpaste")
	var out bytes.Buffer
	pbpasteCmd.Stdout = &out

	err = pbpasteCmd.Run()
	if err != nil {
		return "", err
	}

	return out.String(), nil
}

func getMeetingTitle() (string, error) {
	cmd := exec.Command("osascript", "-e", `display dialog "Purpose/Title of this meeting?" default answer "" with icon alias "Macintosh HD:Applications:zoom.us.app:Contents:Resources:ZPLogo.icns" buttons {"Cancel", "Continue"} default button "Continue"`)

	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		fmt.Println("Error executing osascript:", err)
		return "", err
	}
	output := out.String()

	// Parse the output to extract button returned and text returned
	// e.g. button returned:Continue, text returned:daily standup
	lines := strings.Split(output, ", ")
	var buttonReturned, textReturned string
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			switch key {
			case "button returned":
				buttonReturned = value
			case "text returned":
				textReturned = value
			}
		}
	}

	if buttonReturned != "Continue" {
		return "", nil
	}
	return textReturned, nil
}

func RoundToNearestMinute(d time.Duration) int {
	minutes := d.Minutes()          // Convert duration to float64 minutes
	return int(math.Round(minutes)) // Round to nearest integer
}
