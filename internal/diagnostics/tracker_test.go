package diagnostics

import (
	"testing"
	"time"
)

func TestTrackerDeadAirDetection(t *testing.T) {
	tracker := New()
	tracker.SetConnected(true, "ws://example")

	threshold := 100 * time.Millisecond

	tracker.RecordStepMessage(false)
	if tracker.IsDeadAir(threshold) {
		t.Fatal("expected dead air to be false without audio activity")
	}

	tracker.RecordAudioLevel(0.02, true)
	if tracker.IsDeadAir(threshold) {
		t.Fatal("expected dead air to be false before threshold duration")
	}

	time.Sleep(threshold + 25*time.Millisecond)

	tracker.RecordStepMessage(false)
	tracker.RecordAudioLevel(0.02, true)

	if !tracker.IsDeadAir(threshold) {
		t.Fatal("expected dead air to be true after sustained audio without words")
	}
}

func TestTrackerDeadAirSuppressedByRecentWord(t *testing.T) {
	tracker := New()
	tracker.SetConnected(true, "ws://example")

	threshold := 100 * time.Millisecond

	time.Sleep(threshold + 25*time.Millisecond)

	tracker.RecordStepMessage(false)
	tracker.RecordAudioLevel(0.02, true)
	tracker.RecordWordMessage("hello", true)

	if tracker.IsDeadAir(threshold) {
		t.Fatal("expected dead air to be false after recent word output")
	}
}
