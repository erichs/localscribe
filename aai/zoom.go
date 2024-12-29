package main

import (
	"bytes"
	"fmt"
	"log"
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

func pollZoomStatus(cfg Config) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	log.Println("zoom status polling start")

	for {
		select {
		case <-cfg.Context.Done():
			log.Println("zoom status polling end")
			return // clean-up/terminate
		case <-ticker.C:
			// Get the current Zoom state
			currentState := getZoomState()

			// Check for state transitions
			if currentState != previousState {
				handleStateTransition(previousState, currentState, cfg)
				previousState = currentState
			}
		}
	}
}

// getZoomState determines the current state of Zoom based on `lsof` output.
func getZoomState() ZoomState {
	// Run the `lsof -i 4UDP` command
	cmd := exec.Command("lsof", "-i", "4UDP")
	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		fmt.Println("Error running lsof:", err)
		return Unknown
	}

	// Filter output for zoom.us
	lines := strings.Split(out.String(), "\n")
	zoomLines := filterLines(lines, "zoom.us")

	// Determine state based on the number of matching lines
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
		meetingDuration := time.Now().Sub(meetingStartTime)
		atomicAppendToFile(cfg.LogFile, fmt.Sprintf("%%%%%% meeting ended: %s\n", meetingDuration))
	case ActiveInMeeting:
		meetingStartTime = time.Now()
		meetingUrl, _ := getMeetingURL()
		atomicAppendToFile(cfg.LogFile, fmt.Sprintf("%%%%%% meeting started: %s\n", meetingUrl))
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
