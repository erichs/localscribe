// Package meetings provides combined meeting detection for Zoom and Google Meet.
package meetings

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	// SQLite driver for Chrome history access.
	_ "github.com/mattn/go-sqlite3"
)

// MeetingType identifies the meeting platform.
type MeetingType int

// MeetingType values identify the meeting platform.
const (
	MeetingTypeZoom MeetingType = iota
	MeetingTypeMeet
)

func (t MeetingType) String() string {
	switch t {
	case MeetingTypeZoom:
		return "zoom"
	case MeetingTypeMeet:
		return "meet"
	default:
		return "unknown"
	}
}

// MeetingInfo contains details about a detected meeting.
type MeetingInfo struct {
	Type      MeetingType
	Code      string // Meeting code (e.g., "abc-defg-hij" for Meet)
	Title     string // Meeting title (from Chrome history for Meet)
	StartTime time.Time
}

// MeetingState tracks the state of each meeting platform.
type MeetingState struct {
	ZoomInMeeting bool
	MeetInMeeting bool
}

// Detector monitors for Zoom and Google Meet meetings using lsof.
type Detector struct {
	onMeetingStart func(info MeetingInfo)
	onMeetingEnd   func(meetingType MeetingType, duration time.Duration)

	state            MeetingState
	zoomMeetingStart time.Time
	meetMeetingStart time.Time
	meetMeetingInfo  MeetingInfo

	// Thresholds
	zoomThreshold int // Number of zoom.us UDP connections to consider "in meeting"
	meetThreshold int // Number of Google Chrome 1e100.net connections to consider "in meeting"
}

// NewDetector creates a new combined meeting detector.
func NewDetector(
	onMeetingStart func(info MeetingInfo),
	onMeetingEnd func(meetingType MeetingType, duration time.Duration),
) *Detector {
	return &Detector{
		onMeetingStart: onMeetingStart,
		onMeetingEnd:   onMeetingEnd,
		zoomThreshold:  2,  // Zoom: 2+ UDP connections = in meeting
		meetThreshold:  10, // Meet: 10+ Google Chrome UDP connections to 1e100.net = in meeting
	}
}

// Start begins monitoring for meetings. Blocks until context is cancelled.
func (d *Detector) Start(ctx context.Context) error {
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
			d.processLsofBatch(lines)
			lines = nil
		} else {
			lines = append(lines, line)
		}
	}

	waitErr := cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil {
		return scanErr
	}
	if waitErr != nil && ctx.Err() == nil {
		return fmt.Errorf("lsof exited: %w", waitErr)
	}
	return nil
}

// processLsofBatch analyzes a batch of lsof output for meeting state changes.
func (d *Detector) processLsofBatch(lines []string) {
	// Count Zoom connections
	zoomCount := 0
	for _, line := range lines {
		if strings.Contains(line, "zoom.us") {
			zoomCount++
		}
	}

	// Count Google Meet connections (Chrome to 1e100.net)
	meetCount := 0
	for _, line := range lines {
		if strings.Contains(line, "Google") && strings.Contains(line, "1e100.net") {
			meetCount++
		}
	}

	// Determine current states
	zoomInMeeting := zoomCount >= d.zoomThreshold
	meetInMeeting := meetCount >= d.meetThreshold

	// Handle Zoom state transitions
	if zoomInMeeting && !d.state.ZoomInMeeting {
		// Zoom meeting started
		d.zoomMeetingStart = time.Now()
		if d.onMeetingStart != nil {
			d.onMeetingStart(MeetingInfo{
				Type:      MeetingTypeZoom,
				StartTime: d.zoomMeetingStart,
			})
		}
	} else if !zoomInMeeting && d.state.ZoomInMeeting {
		// Zoom meeting ended
		if d.onMeetingEnd != nil {
			duration := time.Since(d.zoomMeetingStart)
			d.onMeetingEnd(MeetingTypeZoom, duration)
		}
	}

	// Handle Meet state transitions
	if meetInMeeting && !d.state.MeetInMeeting {
		// Meet meeting started - query Chrome for details
		info := MeetingInfo{
			Type:      MeetingTypeMeet,
			StartTime: time.Now(),
		}
		if code, title, err := getMeetDetailsFromChrome(); err == nil {
			info.Code = code
			info.Title = title
		}
		d.meetMeetingStart = info.StartTime
		d.meetMeetingInfo = info
		if d.onMeetingStart != nil {
			d.onMeetingStart(info)
		}
	} else if !meetInMeeting && d.state.MeetInMeeting {
		// Meet meeting ended
		if d.onMeetingEnd != nil {
			duration := time.Since(d.meetMeetingStart)
			d.onMeetingEnd(MeetingTypeMeet, duration)
		}
	}

	// Update state
	d.state.ZoomInMeeting = zoomInMeeting
	d.state.MeetInMeeting = meetInMeeting
}

// getMeetDetailsFromChrome queries Chrome's History database for recent Meet visits.
func getMeetDetailsFromChrome() (code, title string, err error) {
	// Path to Chrome's History file on macOS
	srcPath := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Google", "Chrome", "Default", "History")
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("chrome_history_%d", time.Now().UnixNano()))

	// Copy the database (Chrome keeps it locked)
	if err := copyFile(srcPath, tmpPath); err != nil {
		return "", "", fmt.Errorf("failed to copy History db: %w", err)
	}
	defer os.Remove(tmpPath)

	// Open the temp copy read-only
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", tmpPath))
	if err != nil {
		return "", "", fmt.Errorf("failed to open temp History db: %w", err)
	}
	defer db.Close()

	// Query for the most recent meet.google.com visit
	query := `
SELECT url, title
FROM urls
WHERE url LIKE '%meet.google.com/%-%-%'
ORDER BY last_visit_time DESC
LIMIT 1
`
	var url, pageTitle string
	if err := db.QueryRow(query).Scan(&url, &pageTitle); err != nil {
		return "", "", fmt.Errorf("failed to query Meet URL: %w", err)
	}

	// Extract meeting code from URL
	// URL format: https://meet.google.com/abc-defg-hij?...
	code = extractMeetCode(url)

	// Extract title from page title
	// Page title format: "Meet - abc-defg-hij" or "Meet - Meeting Name"
	title = extractMeetTitle(pageTitle)

	return code, title, nil
}

// meetCodeRegex matches Meet codes like "abc-defg-hij"
var meetCodeRegex = regexp.MustCompile(`meet\.google\.com/([a-z]+-[a-z]+-[a-z]+)`)

func extractMeetCode(url string) string {
	if matches := meetCodeRegex.FindStringSubmatch(url); len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func extractMeetTitle(pageTitle string) string {
	// Page title is "Meet - <title>" or "Meet - <code>"
	if strings.HasPrefix(pageTitle, "Meet - ") {
		return strings.TrimPrefix(pageTitle, "Meet - ")
	}
	return pageTitle
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	// #nosec G304 -- source path is derived from user profile data.
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// #nosec G304 -- destination path is within user-controlled temp dirs.
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
