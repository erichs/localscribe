// Package diagnostics provides state tracking and diagnostic dumping.
package diagnostics

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Tracker records diagnostic state for debugging hung conditions.
type Tracker struct {
	mu sync.RWMutex

	// Connection state
	connected     bool
	lastConnectAt time.Time
	serverURL     string

	// Message stats
	wordMsgCount    atomic.Uint64
	stepMsgCount    atomic.Uint64
	endWordMsgCount atomic.Uint64
	readyMsgCount   atomic.Uint64
	errorMsgCount   atomic.Uint64
	markerMsgCount  atomic.Uint64
	unknownCount    atomic.Uint64
	emptyWordCount  atomic.Uint64

	// Last server error (for debugging)
	lastServerErr   string
	lastServerErrAt time.Time

	// Unknown message types seen (for debugging)
	unknownTypes   map[string]uint64
	unknownTypesMu sync.Mutex

	// Timing
	lastRecvAt   time.Time
	lastWordAt   time.Time
	lastOutputAt time.Time
	startTime    time.Time

	// Audio state (for dead air detection)
	lastAudioActiveAt  time.Time
	lastAudioLevel     float64
	audioBaseline      float64
	audioBaselineCount int
	audioActiveStreak  int
	lastStepAt         time.Time // Last time we received a Step message

	// Errors
	lastRecvErr   error
	lastRecvErrAt time.Time
	lastSendErr   error
	lastSendErrAt time.Time

	// Audio stats
	chunksSent    atomic.Uint64
	chunksDropped atomic.Uint64

	// State flags
	paused       bool
	reconnecting bool

	// Last word received (for debugging)
	lastWord string
}

const (
	audioBaselineAlpha    = 0.2
	audioActiveFactor     = 1.8
	audioMinActiveRMS     = 0.02
	audioBaselineMinValue = 0.01
	audioActiveMinStreak  = 5
)

// New creates a new diagnostic tracker.
func New() *Tracker {
	return &Tracker{
		startTime:    time.Now(),
		unknownTypes: make(map[string]uint64),
	}
}

// SetConnected records connection state.
func (t *Tracker) SetConnected(connected bool, serverURL string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connected = connected
	t.serverURL = serverURL
	if connected {
		t.lastConnectAt = time.Now()
	}
}

// RecordWordMessage records receipt of a word message.
func (t *Tracker) RecordWordMessage(word string, outputProduced bool) {
	t.wordMsgCount.Add(1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRecvAt = time.Now()
	t.lastWord = word
	if word == "" {
		t.emptyWordCount.Add(1)
	}
	if outputProduced {
		t.lastWordAt = time.Now()
		t.lastOutputAt = time.Now()
		t.updateAudioBaselineLocked()
	}
}

// RecordStepMessage records receipt of a step message.
func (t *Tracker) RecordStepMessage(endOfTurn bool) {
	t.stepMsgCount.Add(1)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.lastRecvAt = now
	t.lastStepAt = now
	if endOfTurn {
		t.lastOutputAt = now
	}
}

// RecordAudioLevel records the most recent audio energy level.
func (t *Tracker) RecordAudioLevel(level float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastAudioLevel = level
	if t.isAudioActiveLocked(level) {
		t.audioActiveStreak++
		if t.audioActiveStreak >= audioActiveMinStreak {
			t.lastAudioActiveAt = time.Now()
		}
	} else {
		t.audioActiveStreak = 0
	}
}

// IsDeadAir returns true if dead air condition is detected:
// - Recent local audio activity (within threshold)
// - No words received for longer than threshold
// - Steps are still flowing (within 5 seconds)
func (t *Tracker) IsDeadAir(threshold time.Duration) bool {
	if threshold <= 0 {
		return false
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	now := time.Now()

	// Steps must be flowing (within 5 seconds)
	if t.lastStepAt.IsZero() || now.Sub(t.lastStepAt) > 5*time.Second {
		return false
	}

	// Audio must have been active recently (within threshold)
	if t.lastAudioActiveAt.IsZero() || now.Sub(t.lastAudioActiveAt) > threshold {
		return false
	}

	// No words for longer than threshold
	if !t.lastWordAt.IsZero() && now.Sub(t.lastWordAt) <= threshold {
		return false
	}

	// If we've never received a word but have been receiving steps with audio activity,
	// check if it's been long enough since connection
	if t.lastWordAt.IsZero() {
		// Need at least threshold duration since connection
		if t.lastConnectAt.IsZero() || now.Sub(t.lastConnectAt) <= threshold {
			return false
		}
	}

	return true
}

// ResetDeadAirTracking resets the audio timing state after a reconnection.
func (t *Tracker) ResetDeadAirTracking() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastAudioActiveAt = time.Time{}
	t.lastAudioLevel = 0
	t.lastStepAt = time.Time{}
	// Keep lastWordAt as it tracks overall session, but reconnection resets it
	t.lastWordAt = time.Time{}
}

func (t *Tracker) updateAudioBaselineLocked() {
	level := t.lastAudioLevel
	if level <= 0 {
		return
	}
	if level < audioBaselineMinValue {
		level = audioBaselineMinValue
	}
	if t.audioBaselineCount == 0 {
		t.audioBaseline = level
		t.audioBaselineCount = 1
		return
	}
	t.audioBaseline = (1-audioBaselineAlpha)*t.audioBaseline + audioBaselineAlpha*level
	t.audioBaselineCount++
}

func (t *Tracker) audioActiveThresholdLocked() float64 {
	threshold := audioMinActiveRMS
	if t.audioBaselineCount > 0 {
		threshold = t.audioBaseline * audioActiveFactor
		if threshold < audioMinActiveRMS {
			threshold = audioMinActiveRMS
		}
	}
	return threshold
}

func (t *Tracker) isAudioActiveLocked(level float64) bool {
	if t.audioBaselineCount == 0 {
		return false
	}
	return level >= t.audioActiveThresholdLocked()
}

// RecordEndWordMessage records receipt of an EndWord message.
func (t *Tracker) RecordEndWordMessage() {
	t.endWordMsgCount.Add(1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRecvAt = time.Now()
}

// RecordReadyMessage records receipt of a Ready message.
func (t *Tracker) RecordReadyMessage() {
	t.readyMsgCount.Add(1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRecvAt = time.Now()
}

// RecordErrorMessage records receipt of an Error message from the server.
func (t *Tracker) RecordErrorMessage(message string) {
	t.errorMsgCount.Add(1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRecvAt = time.Now()
	t.lastServerErr = message
	t.lastServerErrAt = time.Now()
}

// RecordMarkerMessage records receipt of a Marker message.
func (t *Tracker) RecordMarkerMessage() {
	t.markerMsgCount.Add(1)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRecvAt = time.Now()
}

// RecordUnknownMessage records receipt of an unknown message type.
func (t *Tracker) RecordUnknownMessage(msgType string) {
	t.unknownCount.Add(1)
	t.mu.Lock()
	t.lastRecvAt = time.Now()
	t.mu.Unlock()

	t.unknownTypesMu.Lock()
	if msgType == "" {
		msgType = "(empty)"
	}
	t.unknownTypes[msgType]++
	t.unknownTypesMu.Unlock()
}

// RecordRecvError records a receive error.
func (t *Tracker) RecordRecvError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRecvErr = err
	t.lastRecvErrAt = time.Now()
}

// RecordSendError records a send error.
func (t *Tracker) RecordSendError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastSendErr = err
	t.lastSendErrAt = time.Now()
}

// RecordChunkSent records an audio chunk sent.
func (t *Tracker) RecordChunkSent() {
	t.chunksSent.Add(1)
}

// RecordChunkDropped records an audio chunk dropped.
func (t *Tracker) RecordChunkDropped() {
	t.chunksDropped.Add(1)
}

// SetPaused updates the paused state.
func (t *Tracker) SetPaused(paused bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paused = paused
}

// SetReconnecting updates the reconnecting state.
func (t *Tracker) SetReconnecting(reconnecting bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reconnecting = reconnecting
}

// DumpToFile writes diagnostic info to the specified file.
func (t *Tracker) DumpToFile(path string) error {
	content := t.Format()
	return os.WriteFile(path, []byte(content), 0600)
}

// Format returns a formatted diagnostic report.
func (t *Tracker) Format() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := time.Now()
	var b strings.Builder

	b.WriteString("=== LOCALSCRIBE DIAGNOSTIC DUMP ===\n")
	b.WriteString(fmt.Sprintf("Timestamp: %s\n", now.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("Uptime: %s\n\n", now.Sub(t.startTime).Round(time.Second)))

	// Connection state
	b.WriteString("--- CONNECTION STATE ---\n")
	b.WriteString(fmt.Sprintf("Server URL: %s\n", t.serverURL))
	b.WriteString(fmt.Sprintf("Connected: %v\n", t.connected))
	if !t.lastConnectAt.IsZero() {
		b.WriteString(fmt.Sprintf("Connected at: %s (%s ago)\n",
			t.lastConnectAt.Format(time.RFC3339),
			now.Sub(t.lastConnectAt).Round(time.Second)))
	}
	b.WriteString(fmt.Sprintf("Paused: %v\n", t.paused))
	b.WriteString(fmt.Sprintf("Reconnecting: %v\n", t.reconnecting))
	b.WriteString("\n")

	// Message stats
	b.WriteString("--- MESSAGE STATISTICS ---\n")
	b.WriteString(fmt.Sprintf("Word messages: %d\n", t.wordMsgCount.Load()))
	b.WriteString(fmt.Sprintf("EndWord messages: %d\n", t.endWordMsgCount.Load()))
	b.WriteString(fmt.Sprintf("Step messages: %d\n", t.stepMsgCount.Load()))
	b.WriteString(fmt.Sprintf("Ready messages: %d\n", t.readyMsgCount.Load()))
	b.WriteString(fmt.Sprintf("Error messages: %d\n", t.errorMsgCount.Load()))
	b.WriteString(fmt.Sprintf("Marker messages: %d\n", t.markerMsgCount.Load()))
	b.WriteString(fmt.Sprintf("Unknown messages: %d\n", t.unknownCount.Load()))
	b.WriteString(fmt.Sprintf("Empty words: %d\n", t.emptyWordCount.Load()))

	// Show server errors if any
	if t.lastServerErr != "" {
		b.WriteString(fmt.Sprintf("Last server error: %q at %s\n",
			t.lastServerErr, t.lastServerErrAt.Format(time.RFC3339)))
	}

	// Show unknown message types breakdown
	t.unknownTypesMu.Lock()
	if len(t.unknownTypes) > 0 {
		b.WriteString("Unknown message types breakdown:\n")
		for msgType, count := range t.unknownTypes {
			b.WriteString(fmt.Sprintf("  - %q: %d\n", msgType, count))
		}
	}
	t.unknownTypesMu.Unlock()
	b.WriteString("\n")

	// Timing analysis (CRITICAL for hung diagnosis)
	b.WriteString("--- TIMING ANALYSIS ---\n")
	if !t.lastRecvAt.IsZero() {
		sinceLast := now.Sub(t.lastRecvAt)
		b.WriteString(fmt.Sprintf("Last message received: %s ago\n", sinceLast.Round(time.Millisecond)))
		if sinceLast > 5*time.Second {
			b.WriteString("  ⚠️  WARNING: No messages received in >5 seconds\n")
		}
	} else {
		b.WriteString("Last message received: NEVER\n")
		b.WriteString("  ⚠️  WARNING: No messages ever received\n")
	}

	if !t.lastWordAt.IsZero() {
		sinceWord := now.Sub(t.lastWordAt)
		b.WriteString(fmt.Sprintf("Last word output: %s ago\n", sinceWord.Round(time.Millisecond)))
		if sinceWord > 10*time.Second {
			b.WriteString("  ⚠️  WARNING: No word output in >10 seconds\n")
		}
	} else {
		b.WriteString("Last word output: NEVER\n")
	}

	if !t.lastOutputAt.IsZero() {
		sinceOutput := now.Sub(t.lastOutputAt)
		b.WriteString(fmt.Sprintf("Last any output: %s ago\n", sinceOutput.Round(time.Millisecond)))
	}

	if !t.lastStepAt.IsZero() {
		sinceStep := now.Sub(t.lastStepAt)
		b.WriteString(fmt.Sprintf("Last step message: %s ago\n", sinceStep.Round(time.Millisecond)))
	}

	if !t.lastAudioActiveAt.IsZero() {
		sinceAudio := now.Sub(t.lastAudioActiveAt)
		b.WriteString(fmt.Sprintf("Last audio activity: %s ago\n", sinceAudio.Round(time.Millisecond)))
	} else {
		b.WriteString("Last audio activity: NEVER\n")
	}
	b.WriteString(fmt.Sprintf("Last audio level (RMS): %.5f\n", t.lastAudioLevel))
	if t.audioBaselineCount > 0 {
		b.WriteString(fmt.Sprintf("Audio baseline (RMS): %.5f (%d samples)\n", t.audioBaseline, t.audioBaselineCount))
		b.WriteString(fmt.Sprintf("Audio activity threshold (RMS): %.5f\n", t.audioActiveThresholdLocked()))
		b.WriteString(fmt.Sprintf("Audio activity streak: %d/%d\n", t.audioActiveStreak, audioActiveMinStreak))
	} else {
		b.WriteString("Audio baseline (RMS): UNSET\n")
		b.WriteString(fmt.Sprintf("Audio activity threshold (RMS): %.5f\n", t.audioActiveThresholdLocked()))
		b.WriteString(fmt.Sprintf("Audio activity streak: %d/%d\n", t.audioActiveStreak, audioActiveMinStreak))
	}

	if t.lastWord != "" {
		b.WriteString(fmt.Sprintf("Last word text: %q\n", t.lastWord))
	}
	b.WriteString("\n")

	// Audio stats
	b.WriteString("--- AUDIO STATISTICS ---\n")
	sent := t.chunksSent.Load()
	dropped := t.chunksDropped.Load()
	b.WriteString(fmt.Sprintf("Chunks sent: %d\n", sent))
	b.WriteString(fmt.Sprintf("Chunks dropped: %d\n", dropped))
	if sent+dropped > 0 {
		dropRate := float64(dropped) / float64(sent+dropped) * 100
		b.WriteString(fmt.Sprintf("Drop rate: %.2f%%\n", dropRate))
		if dropRate > 5 {
			b.WriteString("  ⚠️  WARNING: High audio drop rate\n")
		}
	}
	b.WriteString("\n")

	// Errors
	b.WriteString("--- ERRORS ---\n")
	if t.lastRecvErr != nil {
		b.WriteString(fmt.Sprintf("Last receive error: %v\n", t.lastRecvErr))
		b.WriteString(fmt.Sprintf("  At: %s (%s ago)\n",
			t.lastRecvErrAt.Format(time.RFC3339),
			now.Sub(t.lastRecvErrAt).Round(time.Second)))
	} else {
		b.WriteString("Last receive error: none\n")
	}

	if t.lastSendErr != nil {
		b.WriteString(fmt.Sprintf("Last send error: %v\n", t.lastSendErr))
		b.WriteString(fmt.Sprintf("  At: %s (%s ago)\n",
			t.lastSendErrAt.Format(time.RFC3339),
			now.Sub(t.lastSendErrAt).Round(time.Second)))
	} else {
		b.WriteString("Last send error: none\n")
	}
	b.WriteString("\n")

	// Goroutine info
	b.WriteString("--- GOROUTINE INFO ---\n")
	b.WriteString(fmt.Sprintf("Active goroutines: %d\n", runtime.NumGoroutine()))
	b.WriteString("\n")

	// Stack traces (truncated)
	b.WriteString("--- GOROUTINE STACKS (truncated) ---\n")
	buf := make([]byte, 64*1024)
	n := runtime.Stack(buf, true)
	stacks := string(buf[:n])
	// Truncate if too long
	if len(stacks) > 16000 {
		stacks = stacks[:16000] + "\n... (truncated)\n"
	}
	b.WriteString(stacks)
	b.WriteString("\n")

	// Diagnosis suggestions
	b.WriteString("--- DIAGNOSIS SUGGESTIONS ---\n")
	t.writeDiagnosis(&b, now)

	return b.String()
}

func (t *Tracker) writeDiagnosis(b *strings.Builder, now time.Time) {
	issues := []string{}

	// Check for stalled receive
	if !t.lastRecvAt.IsZero() && now.Sub(t.lastRecvAt) > 5*time.Second {
		if t.connected && !t.paused && !t.reconnecting {
			issues = append(issues,
				"LIKELY CAUSE: Receive goroutine blocked. Server may have stopped sending, "+
					"or WebSocket read is hung. Check if moshi-server is still processing.")
		}
	}

	// Check for messages received but no output
	wordCount := t.wordMsgCount.Load()
	if wordCount > 0 && t.lastWordAt.IsZero() {
		issues = append(issues,
			"LIKELY CAUSE: Word messages received but no output produced. "+
				"Words may all be empty or PostProcessor filtering them out.")
	}

	// Check for high empty word rate
	emptyCount := t.emptyWordCount.Load()
	if wordCount > 0 && float64(emptyCount)/float64(wordCount) > 0.9 {
		issues = append(issues,
			"LIKELY CAUSE: >90% of word messages are empty. Server may be sending empty words.")
	}

	// Check for output gap despite recent messages
	if !t.lastRecvAt.IsZero() && now.Sub(t.lastRecvAt) < 2*time.Second {
		if !t.lastOutputAt.IsZero() && now.Sub(t.lastOutputAt) > 10*time.Second {
			issues = append(issues,
				"LIKELY CAUSE: Messages still arriving but no output. Check PostProcessor state "+
					"or whether message types are being handled correctly.")
		}
	}

	// Check for unknown messages
	if t.unknownCount.Load() > 0 {
		issues = append(issues,
			fmt.Sprintf("NOTE: %d unknown message type(s) received. These are silently ignored.",
				t.unknownCount.Load()))
	}

	if len(issues) == 0 {
		b.WriteString("No obvious issues detected from metrics alone.\n")
		b.WriteString("Check goroutine stacks above for blocked operations.\n")
	} else {
		for i, issue := range issues {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, issue))
		}
	}
}
