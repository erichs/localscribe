package plugins

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"localscribe/internal/config"
	"localscribe/internal/meetings"
)

// mockWriter captures metadata writes for testing.
type mockWriter struct {
	mu       sync.Mutex
	metadata []string
}

func (m *mockWriter) WriteMetadata(data string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metadata = append(m.metadata, data)
	return nil
}

func (m *mockWriter) getMetadata() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.metadata))
	copy(result, m.metadata)
	return result
}

func TestRunner_Execute_SimpleCommand(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{
			Name:    "test-plugin",
			Command: "echo hello world",
			Trigger: config.TriggerOnStart,
		},
	}

	runner := NewRunner(plugins, writer, false, stderr)
	ctx := context.Background()

	runner.Execute(ctx, config.TriggerOnStart, nil)

	metadata := writer.getMetadata()
	if len(metadata) != 1 {
		t.Fatalf("expected 1 metadata line, got %d", len(metadata))
	}

	expected := "%% test-plugin: hello world\n"
	if metadata[0] != expected {
		t.Errorf("expected %q, got %q", expected, metadata[0])
	}
}

func TestRunner_Execute_MultiLineOutput(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{
			Name:    "multi-line",
			Command: "echo 'line1'; echo 'line2'; echo 'line3'",
			Trigger: config.TriggerOnStart,
		},
	}

	runner := NewRunner(plugins, writer, false, stderr)
	ctx := context.Background()

	runner.Execute(ctx, config.TriggerOnStart, nil)

	metadata := writer.getMetadata()
	if len(metadata) != 3 {
		t.Fatalf("expected 3 metadata lines, got %d: %v", len(metadata), metadata)
	}

	expectedLines := []string{
		"%% multi-line: line1\n",
		"%% multi-line: line2\n",
		"%% multi-line: line3\n",
	}

	for i, expected := range expectedLines {
		if metadata[i] != expected {
			t.Errorf("line %d: expected %q, got %q", i, expected, metadata[i])
		}
	}
}

func TestRunner_Execute_SkipsEmptyLines(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{
			Name:    "with-empty",
			Command: "echo 'line1'; echo ''; echo 'line2'",
			Trigger: config.TriggerOnStart,
		},
	}

	runner := NewRunner(plugins, writer, false, stderr)
	ctx := context.Background()

	runner.Execute(ctx, config.TriggerOnStart, nil)

	metadata := writer.getMetadata()
	if len(metadata) != 2 {
		t.Fatalf("expected 2 metadata lines (empty skipped), got %d: %v", len(metadata), metadata)
	}
}

func TestRunner_Execute_Timeout(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{
			Name:    "slow-plugin",
			Command: "sleep 10",
			Trigger: config.TriggerOnStart,
			Timeout: config.Duration(100 * time.Millisecond),
		},
	}

	runner := NewRunner(plugins, writer, false, stderr)
	ctx := context.Background()

	start := time.Now()
	runner.Execute(ctx, config.TriggerOnStart, nil)
	elapsed := time.Since(start)

	// Should complete quickly due to timeout
	if elapsed > 2*time.Second {
		t.Errorf("expected quick timeout, but took %v", elapsed)
	}

	// Should have logged timeout error
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "timed out") {
		t.Errorf("expected timeout error in stderr, got: %s", stderrStr)
	}
}

func TestRunner_Execute_NonZeroExit(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{
			Name:    "failing-plugin",
			Command: "exit 1",
			Trigger: config.TriggerOnStart,
		},
	}

	runner := NewRunner(plugins, writer, false, stderr)
	ctx := context.Background()

	runner.Execute(ctx, config.TriggerOnStart, nil)

	// Should have logged error
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "exited with error") {
		t.Errorf("expected exit error in stderr, got: %s", stderrStr)
	}
}

func TestRunner_Execute_OnlyMatchingTrigger(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{
			Name:    "on-start",
			Command: "echo 'start'",
			Trigger: config.TriggerOnStart,
		},
		{
			Name:    "on-meeting",
			Command: "echo 'meeting'",
			Trigger: config.TriggerOnMeetingStart,
		},
	}

	runner := NewRunner(plugins, writer, false, stderr)
	ctx := context.Background()

	runner.Execute(ctx, config.TriggerOnStart, nil)

	metadata := writer.getMetadata()
	if len(metadata) != 1 {
		t.Fatalf("expected 1 metadata line, got %d", len(metadata))
	}

	if !strings.Contains(metadata[0], "start") {
		t.Errorf("expected 'start' plugin output, got: %s", metadata[0])
	}
}

func TestRunner_Execute_EnvironmentVariables(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{
			Name:    "env-test",
			Command: "echo $LOCALDSMC_EVENT",
			Trigger: config.TriggerOnMeetingStart,
		},
	}

	runner := NewRunner(plugins, writer, false, stderr)
	ctx := context.Background()

	execCtx := &ExecuteContext{
		OutputFile:   "/tmp/test.txt",
		MeetingType:  meetings.MeetingTypeZoom,
		MeetingCode:  "123-456-789",
		MeetingTitle: "Test Meeting",
	}

	runner.Execute(ctx, config.TriggerOnMeetingStart, execCtx)

	metadata := writer.getMetadata()
	if len(metadata) != 1 {
		t.Fatalf("expected 1 metadata line, got %d", len(metadata))
	}

	if !strings.Contains(metadata[0], "on_meeting_start") {
		t.Errorf("expected LOCALDSMC_EVENT in output, got: %s", metadata[0])
	}
}

func TestRunner_HasPlugins(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	// Empty plugins
	runner1 := NewRunner(nil, writer, false, stderr)
	if runner1.HasPlugins() {
		t.Error("expected HasPlugins() to return false for empty plugins")
	}

	// With plugins
	runner2 := NewRunner([]config.PluginConfig{{Name: "test"}}, writer, false, stderr)
	if !runner2.HasPlugins() {
		t.Error("expected HasPlugins() to return true with plugins")
	}
}

func TestRunner_HasTrigger(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{Name: "p1", Trigger: config.TriggerOnStart},
		{Name: "p2", Trigger: config.TriggerPeriodic},
	}

	runner := NewRunner(plugins, writer, false, stderr)

	if !runner.HasTrigger(config.TriggerOnStart) {
		t.Error("expected HasTrigger(on_start) to return true")
	}
	if !runner.HasTrigger(config.TriggerPeriodic) {
		t.Error("expected HasTrigger(periodic) to return true")
	}
	if runner.HasTrigger(config.TriggerOnMeetingStart) {
		t.Error("expected HasTrigger(on_meeting_start) to return false")
	}
}

func TestRunner_StartPeriodic(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{
			Name:     "periodic-test",
			Command:  "echo 'tick'",
			Trigger:  config.TriggerPeriodic,
			Interval: 1, // 1 second
		},
	}

	runner := NewRunner(plugins, writer, false, stderr)
	ctx, cancel := context.WithCancel(context.Background())

	runner.StartPeriodic(ctx, nil)

	// Wait for at least 2 ticks
	time.Sleep(2500 * time.Millisecond)

	runner.StopPeriodic()
	cancel()

	metadata := writer.getMetadata()
	// Should have at least 2 periodic outputs
	if len(metadata) < 2 {
		t.Errorf("expected at least 2 periodic outputs, got %d", len(metadata))
	}
}

func TestRunner_Execute_Parallel(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	// Create multiple slow plugins that each take 100ms
	plugins := []config.PluginConfig{
		{Name: "slow1", Command: "sleep 0.1 && echo 'slow1'", Trigger: config.TriggerOnStart},
		{Name: "slow2", Command: "sleep 0.1 && echo 'slow2'", Trigger: config.TriggerOnStart},
		{Name: "slow3", Command: "sleep 0.1 && echo 'slow3'", Trigger: config.TriggerOnStart},
	}

	runner := NewRunner(plugins, writer, false, stderr)
	ctx := context.Background()

	start := time.Now()
	runner.Execute(ctx, config.TriggerOnStart, nil)
	elapsed := time.Since(start)

	// If running in parallel, should complete in ~100ms, not 300ms
	if elapsed > 250*time.Millisecond {
		t.Errorf("expected parallel execution (~100ms), but took %v", elapsed)
	}

	metadata := writer.getMetadata()
	if len(metadata) != 3 {
		t.Errorf("expected 3 outputs, got %d", len(metadata))
	}
}

func TestRunner_DebugMode(t *testing.T) {
	writer := &mockWriter{}
	stderr := &bytes.Buffer{}

	plugins := []config.PluginConfig{
		{
			Name:    "debug-test",
			Command: "echo 'stdout' && echo 'stderr' >&2",
			Trigger: config.TriggerOnStart,
		},
	}

	runner := NewRunner(plugins, writer, true, stderr) // debug mode enabled
	ctx := context.Background()

	runner.Execute(ctx, config.TriggerOnStart, nil)

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "stderr") {
		t.Errorf("expected stderr in debug output, got: %s", stderrStr)
	}
}
