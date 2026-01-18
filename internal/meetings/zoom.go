// Package meetings provides meeting detection for various platforms.
package meetings

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"
)

// ZoomState represents the current state of Zoom.
type ZoomState int

// ZoomState values describe Zoom activity.
const (
	ZoomUnknown ZoomState = iota
	ZoomActiveNoMeeting
	ZoomActiveInMeeting
)

// ZoomDetector monitors Zoom meeting status.
type ZoomDetector struct {
	onMeetingStart func()
	onMeetingEnd   func(duration time.Duration)
	previousState  ZoomState
	meetingStart   time.Time
}

// NewZoomDetector creates a new Zoom meeting detector.
// onMeetingStart is called when a meeting starts.
// onMeetingEnd is called when a meeting ends with the duration.
func NewZoomDetector(onMeetingStart func(), onMeetingEnd func(duration time.Duration)) *ZoomDetector {
	return &ZoomDetector{
		onMeetingStart: onMeetingStart,
		onMeetingEnd:   onMeetingEnd,
		previousState:  ZoomUnknown,
	}
}

// Start begins monitoring Zoom status. Blocks until context is cancelled.
func (d *ZoomDetector) Start(ctx context.Context) error {
	// Set up the lsof command in repeat mode
	cmd := exec.CommandContext(ctx, "lsof", "-i", "4UDP", "-r", "5")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("error creating stdout pipe for lsof: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("error starting lsof: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	var zoomLines []string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "=======" {
			// Process batch of output when delimiter is encountered
			currentState := d.determineState(zoomLines)
			if currentState != d.previousState {
				d.handleStateTransition(d.previousState, currentState)
				d.previousState = currentState
			}
			zoomLines = nil
		} else {
			zoomLines = append(zoomLines, line)
		}
	}

	// Wait for command to finish
	waitErr := cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil {
		return scanErr
	}
	if waitErr != nil && ctx.Err() == nil {
		return fmt.Errorf("lsof exited: %w", waitErr)
	}
	return nil
}

// determineState determines the current Zoom state based on lsof output.
func (d *ZoomDetector) determineState(lines []string) ZoomState {
	zoomCount := 0
	for _, line := range lines {
		if strings.Contains(line, "zoom.us") {
			zoomCount++
		}
	}

	switch {
	case zoomCount == 1:
		return ZoomActiveNoMeeting
	case zoomCount >= 2:
		return ZoomActiveInMeeting
	default:
		return ZoomUnknown
	}
}

// handleStateTransition performs actions based on state transitions.
func (d *ZoomDetector) handleStateTransition(previous, current ZoomState) {
	switch current {
	case ZoomActiveNoMeeting:
		if previous == ZoomUnknown {
			return // Ignore initial transition on startup
		}
		if d.onMeetingEnd != nil {
			duration := time.Since(d.meetingStart)
			d.onMeetingEnd(duration)
		}

	case ZoomActiveInMeeting:
		d.meetingStart = time.Now()
		if d.onMeetingStart != nil {
			d.onMeetingStart()
		}
	}
}

// RoundToNearestMinute rounds a duration to the nearest minute.
func RoundToNearestMinute(d time.Duration) int {
	minutes := d.Minutes()
	return int(math.Round(minutes))
}
