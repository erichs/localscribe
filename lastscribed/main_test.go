package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeartbeatRegex(t *testing.T) {
	// Test that the heartbeat regex matches the correct format (double %%)
	testCases := []struct {
		input    string
		expected bool
	}{
		{"%% time: 2026/01/17 23:04:29 EST", true},
		{"%%time: 2026/01/17 23:04:29 EST", true},
		{"%% time:2026/01/17 23:04:29 EST", true},
		{"% time: 2026/01/17 23:04:29 EST", false},  // Single % should NOT match
		{"%%% time: 2026/01/17 23:04:29 EST", false}, // Triple % should NOT match
		{"time: 2026/01/17 23:04:29 EST", false},     // No prefix should NOT match
	}

	for _, tc := range testCases {
		match := heartbeatRegex.MatchString(tc.input)
		if match != tc.expected {
			t.Errorf("heartbeatRegex.MatchString(%q) = %v, want %v", tc.input, match, tc.expected)
		}
	}
}

func TestMeetingStartRegex(t *testing.T) {
	// Test that the meeting start regex matches the correct format (double %%)
	testCases := []struct {
		input    string
		expected bool
	}{
		{"%% meeting started: 2026/01/17 23:04:29 EST zoom", true},
		{"%% meeting started: 2026/01/17 23:04:29 EST meet/abc-def-ghi", true},
		{"%%meeting started: 2026/01/17 23:04:29 EST", true},
		{"% meeting started: 2026/01/17 23:04:29 EST zoom", false}, // Single % should NOT match
		{"meeting started: 2026/01/17 23:04:29 EST zoom", false},   // No prefix should NOT match
	}

	for _, tc := range testCases {
		match := newMeetingStartRegex.MatchString(tc.input)
		if match != tc.expected {
			t.Errorf("newMeetingStartRegex.MatchString(%q) = %v, want %v", tc.input, match, tc.expected)
		}
	}
}

func TestMeetingEndRegex(t *testing.T) {
	// Test that the meeting end regex matches the correct format (double %%)
	testCases := []struct {
		input    string
		expected bool
	}{
		{"%% meeting ended: 2026/01/17 23:04:29 EST zoom (duration: 45m)", true},
		{"%% meeting ended: 2026/01/17 23:04:29 EST meet (duration: 1m)", true},
		{"%%meeting ended: 2026/01/17 23:04:29 EST", true},
		{"% meeting ended: 2026/01/17 23:04:29 EST zoom", false}, // Single % should NOT match
		{"meeting ended: 2026/01/17 23:04:29 EST", false},        // No prefix should NOT match
	}

	for _, tc := range testCases {
		match := newMeetingEndRegex.MatchString(tc.input)
		if match != tc.expected {
			t.Errorf("newMeetingEndRegex.MatchString(%q) = %v, want %v", tc.input, match, tc.expected)
		}
	}
}

func TestParseLogLineWithHeartbeat(t *testing.T) {
	// Test parsing of heartbeat lines
	heartbeat := time.Time{}

	// Test heartbeat line
	line, ok := parseLogLineWithHeartbeat("%% time: 2026/01/17 23:04:29 EST", &heartbeat)
	if !ok {
		t.Error("parseLogLineWithHeartbeat should return true for heartbeat line")
	}
	if !line.IsMetadata {
		t.Error("heartbeat line should be marked as metadata")
	}
	if heartbeat.IsZero() {
		t.Error("heartbeat should be updated after parsing heartbeat line")
	}

	// Test meeting start line
	line, ok = parseLogLineWithHeartbeat("%% meeting started: 2026/01/17 23:04:29 EST zoom", &heartbeat)
	if !ok {
		t.Error("parseLogLineWithHeartbeat should return true for meeting start line")
	}
	if !line.IsMeetingStart {
		t.Error("meeting start line should be marked as IsMeetingStart")
	}
	if !line.IsMetadata {
		t.Error("meeting start line should be marked as metadata")
	}

	// Test meeting end line
	line, ok = parseLogLineWithHeartbeat("%% meeting ended: 2026/01/17 23:04:29 EST zoom (duration: 45m)", &heartbeat)
	if !ok {
		t.Error("parseLogLineWithHeartbeat should return true for meeting end line")
	}
	if !line.IsMeetingEnd {
		t.Error("meeting end line should be marked as IsMeetingEnd")
	}
	if !line.IsMetadata {
		t.Error("meeting end line should be marked as metadata")
	}

	// Test plain transcript line (uses current heartbeat)
	line, ok = parseLogLineWithHeartbeat("Hello, this is a transcript line.", &heartbeat)
	if !ok {
		t.Error("parseLogLineWithHeartbeat should return true for plain text with valid heartbeat")
	}
	if line.IsMetadata {
		t.Error("plain transcript line should NOT be marked as metadata")
	}
	if line.Timestamp.IsZero() {
		t.Error("plain transcript line should have timestamp from heartbeat")
	}
}

func TestReadAndParseAllNewFormat(t *testing.T) {
	// Create a temporary directory with test transcript files
	tmpDir, err := os.MkdirTemp("", "lastscribed_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test transcript file in the new format
	testContent := `%% time: 2026/01/17 20:00:00 EST
Hello world.
This is a test.
%% time: 2026/01/17 20:01:00 EST
More transcript text.
%% meeting started: 2026/01/17 20:02:00 EST zoom
Meeting content here.
%% meeting ended: 2026/01/17 20:03:00 EST zoom (duration: 1m)
Post-meeting text.
`
	testFile := filepath.Join(tmpDir, "transcript_20260117_200000.txt")
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Parse the files
	lines, err := readAndParseAll(tmpDir)
	if err != nil {
		t.Fatalf("readAndParseAll failed: %v", err)
	}

	// Verify results
	if len(lines) == 0 {
		t.Fatal("Expected some lines to be parsed")
	}

	// Count metadata and meeting markers
	metadataCount := 0
	meetingStartCount := 0
	meetingEndCount := 0
	for _, ln := range lines {
		if ln.IsMetadata {
			metadataCount++
		}
		if ln.IsMeetingStart {
			meetingStartCount++
		}
		if ln.IsMeetingEnd {
			meetingEndCount++
		}
	}

	if metadataCount < 4 {
		t.Errorf("Expected at least 4 metadata lines (2 heartbeats + 1 meeting start + 1 meeting end), got %d", metadataCount)
	}
	if meetingStartCount != 1 {
		t.Errorf("Expected 1 meeting start, got %d", meetingStartCount)
	}
	if meetingEndCount != 1 {
		t.Errorf("Expected 1 meeting end, got %d", meetingEndCount)
	}
}

func TestRemoveAllMetadata(t *testing.T) {
	lines := []LogLine{
		{Raw: "%% time: 2026/01/17 20:00:00 EST", IsMetadata: true},
		{Raw: "Transcript text", IsMetadata: false},
		{Raw: "%% meeting started: 2026/01/17 20:02:00 EST zoom", IsMetadata: true, IsMeetingStart: true},
		{Raw: "More text", IsMetadata: false},
		{Raw: "%% meeting ended: 2026/01/17 20:03:00 EST zoom", IsMetadata: true, IsMeetingEnd: true},
	}

	filtered := removeAllMetadata(lines)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 lines after removing metadata, got %d", len(filtered))
	}

	for _, ln := range filtered {
		if ln.IsMetadata {
			t.Error("Filtered lines should not contain metadata")
		}
	}
}

func TestFindMeetingIntervals(t *testing.T) {
	now := time.Now()
	lines := []LogLine{
		{Timestamp: now, Raw: "Before meeting", IsMetadata: false},
		{Timestamp: now.Add(1 * time.Minute), Raw: "%% meeting started: ...", IsMetadata: true, IsMeetingStart: true},
		{Timestamp: now.Add(2 * time.Minute), Raw: "During meeting", IsMetadata: false},
		{Timestamp: now.Add(3 * time.Minute), Raw: "%% meeting ended: ...", IsMetadata: true, IsMeetingEnd: true},
		{Timestamp: now.Add(4 * time.Minute), Raw: "After meeting", IsMetadata: false},
	}

	intervals := findMeetingIntervals(lines)

	if len(intervals) != 1 {
		t.Fatalf("Expected 1 meeting interval, got %d", len(intervals))
	}

	interval := intervals[0]
	if interval.StartIndex != 1 {
		t.Errorf("Expected start index 1, got %d", interval.StartIndex)
	}
	if interval.EndIndex != 3 {
		t.Errorf("Expected end index 3, got %d", interval.EndIndex)
	}
}

func TestMetadataLinePrefix(t *testing.T) {
	// Test that lines starting with %% are recognized as metadata
	heartbeat := time.Now()

	testCases := []struct {
		input      string
		isMetadata bool
	}{
		{"%% time: 2026/01/17 20:00:00 EST", true},
		{"%% meeting started: 2026/01/17 20:00:00 EST", true},
		{"%% meeting ended: 2026/01/17 20:00:00 EST", true},
		{"%% meeting title: Weekly Standup", true},
		{"%% custom metadata", true},
		{"% time: 2026/01/17 20:00:00 EST", false}, // Single % is NOT metadata in new format
		{"Regular transcript text", false},
	}

	for _, tc := range testCases {
		line, ok := parseLogLineWithHeartbeat(tc.input, &heartbeat)
		if !ok && tc.isMetadata {
			// Should be parsed if it starts with %%
			if strings.HasPrefix(tc.input, "%%") {
				t.Errorf("Line %q should be parsed", tc.input)
			}
			continue
		}
		if ok && line.IsMetadata != tc.isMetadata {
			t.Errorf("Line %q: IsMetadata = %v, want %v", tc.input, line.IsMetadata, tc.isMetadata)
		}
	}
}

func TestReadAndParseAllSkipsCorruptedFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lastscribed_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a valid file
	validContent := "%% time: 2026/01/17 20:00:00 EST\nHello world.\n"
	validFile := filepath.Join(tmpDir, "transcript_valid.txt")
	os.WriteFile(validFile, []byte(validContent), 0644)

	// Create a file with a very long line (over 1MB to exceed even increased buffer)
	longLine := strings.Repeat("x", 2*1024*1024) // 2MB line
	corruptFile := filepath.Join(tmpDir, "transcript_corrupt.txt")
	os.WriteFile(corruptFile, []byte(longLine), 0644)

	// Should still return lines from valid file
	lines, err := readAndParseAll(tmpDir)
	if err != nil {
		t.Fatalf("readAndParseAll should not return error: %v", err)
	}
	if len(lines) == 0 {
		t.Error("Expected lines from valid file, got none")
	}
}

func TestScannerWithLargeBuffer(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lastscribed_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a file with a 100KB line (exceeds default 64KB but within 1MB)
	longLine := "%% time: 2026/01/17 20:00:00 EST\n" + strings.Repeat("x", 100*1024) + "\n"
	testFile := filepath.Join(tmpDir, "transcript_long.txt")
	os.WriteFile(testFile, []byte(longLine), 0644)

	lines, err := readAndParseAll(tmpDir)
	if err != nil {
		t.Fatalf("readAndParseAll failed on 100KB line: %v", err)
	}
	if len(lines) < 2 {
		t.Errorf("Expected at least 2 lines, got %d", len(lines))
	}
}
