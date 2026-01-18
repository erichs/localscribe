package meetings

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// MeetState represents the current state of Google Meet.
type MeetState int

const (
	MeetNotInMeeting MeetState = iota
	MeetInMeeting
)

// MeetDetector monitors Google Meet status via lsof UDP connections.
// Google Meet creates many UDP connections to *.1e100.net when in a meeting.
type MeetDetector struct {
	onMeetingStart func()
	onMeetingEnd   func(duration time.Duration)
	previousState  MeetState
	meetingStart   time.Time
	// Threshold for number of Google Chrome UDP connections to consider "in meeting"
	connectionThreshold int
}

// NewMeetDetector creates a new Google Meet detector.
// onMeetingStart is called when a meeting starts.
// onMeetingEnd is called when a meeting ends with the duration.
func NewMeetDetector(onMeetingStart func(meetCode string), onMeetingEnd func(duration time.Duration)) *MeetDetector {
	return &MeetDetector{
		onMeetingStart: func() {
			// Wrap to match old signature (no meet code available via lsof)
			onMeetingStart("")
		},
		onMeetingEnd:        onMeetingEnd,
		previousState:       MeetNotInMeeting,
		connectionThreshold: 10, // 10+ Google Chrome UDP connections = in Meet (baseline: 6-7, during Meet: 14-16)
	}
}

// Start begins monitoring Google Meet status via lsof. Blocks until context is cancelled.
func (d *MeetDetector) Start(ctx context.Context) error {
	// Set up lsof command in repeat mode (like Zoom detection)
	cmd := exec.CommandContext(ctx, "lsof", "-i", "4UDP", "-r", "5")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("error creating stdout pipe for lsof: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("error starting lsof: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	var lines []string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "=======" {
			// Process batch of output when delimiter is encountered
			currentState := d.determineState(lines)
			if currentState != d.previousState {
				d.handleTransition(d.previousState, currentState)
				d.previousState = currentState
			}
			lines = nil
		} else {
			lines = append(lines, line)
		}
	}

	cmd.Wait()
	return scanner.Err()
}

// determineState determines the current Meet state based on lsof output.
// Counts Google Chrome UDP connections to 1e100.net (Google's infrastructure).
func (d *MeetDetector) determineState(lines []string) MeetState {
	googleChromeConnections := 0
	for _, line := range lines {
		// Look for Google Chrome connections to Google's servers
		if strings.Contains(line, "Google") && strings.Contains(line, "1e100.net") {
			googleChromeConnections++
		}
	}

	// When in a Meet, there are many (30+) UDP connections
	// When not in a Meet, there's typically 0-2
	if googleChromeConnections >= d.connectionThreshold {
		return MeetInMeeting
	}
	return MeetNotInMeeting
}

// handleTransition performs actions based on state transitions.
func (d *MeetDetector) handleTransition(previous, current MeetState) {
	switch current {
	case MeetNotInMeeting:
		if previous == MeetInMeeting && d.onMeetingEnd != nil {
			duration := time.Since(d.meetingStart)
			d.onMeetingEnd(duration)
		}

	case MeetInMeeting:
		d.meetingStart = time.Now()
		if d.onMeetingStart != nil {
			d.onMeetingStart()
		}
	}
}
