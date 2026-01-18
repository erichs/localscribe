// Package plugins provides plugin execution for localscribe.
package plugins

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"localscribe/internal/config"
	"localscribe/internal/meetings"
)

// DefaultTimeout is the default plugin execution timeout.
const DefaultTimeout = 5 * time.Second

// MetadataWriter is the interface for writing metadata to the transcript.
type MetadataWriter interface {
	WriteMetadata(data string) error
}

// Runner executes plugins at defined lifecycle events.
type Runner struct {
	plugins []config.PluginConfig
	writer  MetadataWriter
	debug   bool
	stderr  io.Writer

	// periodicDone channels for stopping periodic plugins
	periodicMu   sync.Mutex
	periodicDone []chan struct{}
}

// NewRunner creates a new plugin runner.
func NewRunner(plugins []config.PluginConfig, writer MetadataWriter, debug bool, stderr io.Writer) *Runner {
	return &Runner{
		plugins: plugins,
		writer:  writer,
		debug:   debug,
		stderr:  stderr,
	}
}

// ExecuteContext holds context passed to plugins via environment variables.
type ExecuteContext struct {
	OutputFile string

	// Meeting context (only set for meeting events)
	MeetingType     meetings.MeetingType
	MeetingCode     string
	MeetingTitle    string
	MeetingDuration time.Duration // only for on_meeting_end
}

// Execute runs all plugins matching the given trigger in parallel.
// Errors are logged to stderr but don't stop other plugins.
func (r *Runner) Execute(ctx context.Context, trigger config.TriggerType, execCtx *ExecuteContext) {
	var matchingPlugins []config.PluginConfig
	for _, p := range r.plugins {
		if p.Trigger == trigger {
			matchingPlugins = append(matchingPlugins, p)
		}
	}

	if len(matchingPlugins) == 0 {
		return
	}

	// Execute plugins in parallel
	var wg sync.WaitGroup
	for _, p := range matchingPlugins {
		wg.Add(1)
		go func(plugin config.PluginConfig) {
			defer wg.Done()
			r.executePlugin(ctx, plugin, trigger, execCtx)
		}(p)
	}
	wg.Wait()
}

// StartPeriodic starts all periodic plugins with their configured intervals.
// Returns immediately; plugins run in background goroutines.
// Call StopPeriodic to stop all periodic plugins.
func (r *Runner) StartPeriodic(ctx context.Context, execCtx *ExecuteContext) {
	r.periodicMu.Lock()
	defer r.periodicMu.Unlock()

	for _, p := range r.plugins {
		if p.Trigger != config.TriggerPeriodic {
			continue
		}

		if p.Interval <= 0 {
			if r.debug {
				fmt.Fprintf(r.stderr, "[DEBUG] Plugin %q has periodic trigger but no interval, skipping\n", p.Name)
			}
			continue
		}

		done := make(chan struct{})
		r.periodicDone = append(r.periodicDone, done)

		go func(plugin config.PluginConfig, done chan struct{}) {
			ticker := time.NewTicker(time.Duration(plugin.Interval) * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-done:
					return
				case <-ticker.C:
					r.executePlugin(ctx, plugin, config.TriggerPeriodic, execCtx)
				}
			}
		}(p, done)
	}
}

// StopPeriodic stops all periodic plugins.
func (r *Runner) StopPeriodic() {
	r.periodicMu.Lock()
	defer r.periodicMu.Unlock()

	for _, done := range r.periodicDone {
		close(done)
	}
	r.periodicDone = nil
}

// executePlugin runs a single plugin and writes its output as metadata.
func (r *Runner) executePlugin(ctx context.Context, plugin config.PluginConfig, trigger config.TriggerType, execCtx *ExecuteContext) {
	timeout := plugin.Timeout.Duration()
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Expand ~ in command path
	command := config.ExpandPath(plugin.Command)

	// Build environment variables
	env := r.buildEnv(trigger, execCtx)

	// Execute via shell to support pipes, redirects, etc.
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = append(os.Environ(), env...)

	// Capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(r.stderr, "[plugin/%s] failed to create stdout pipe: %v\n", plugin.Name, err)
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(r.stderr, "[plugin/%s] failed to create stderr pipe: %v\n", plugin.Name, err)
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(r.stderr, "[plugin/%s] failed to start: %v\n", plugin.Name, err)
		return
	}

	// Read stdout and stderr concurrently
	var wg sync.WaitGroup
	var stdoutLines []string
	var stderrLines []string
	var mu sync.Mutex

	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			mu.Lock()
			stdoutLines = append(stdoutLines, scanner.Text())
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			mu.Lock()
			stderrLines = append(stderrLines, scanner.Text())
			mu.Unlock()
		}
	}()

	wg.Wait()

	// Wait for command to complete
	err = cmd.Wait()

	// Log stderr in debug mode
	if r.debug && len(stderrLines) > 0 {
		for _, line := range stderrLines {
			fmt.Fprintf(r.stderr, "[plugin/%s] stderr: %s\n", plugin.Name, line)
		}
	}

	// Handle errors (but don't crash, just log)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Fprintf(r.stderr, "[plugin/%s] timed out after %v\n", plugin.Name, timeout)
		} else {
			fmt.Fprintf(r.stderr, "[plugin/%s] exited with error: %v\n", plugin.Name, err)
		}
		// Log stderr on error even without debug mode
		if !r.debug && len(stderrLines) > 0 {
			for _, line := range stderrLines {
				fmt.Fprintf(r.stderr, "[plugin/%s] stderr: %s\n", plugin.Name, line)
			}
		}
		return
	}

	// Write stdout lines as metadata
	if r.debug && len(stdoutLines) > 0 {
		fmt.Fprintf(r.stderr, "[DEBUG] Plugin %q produced %d lines of output\n", plugin.Name, len(stdoutLines))
	}

	for _, line := range stdoutLines {
		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Format: %% plugin-name: <line>
		metadata := fmt.Sprintf("%%%% %s: %s\n", plugin.Name, line)
		if err := r.writer.WriteMetadata(metadata); err != nil {
			fmt.Fprintf(r.stderr, "[plugin/%s] failed to write metadata: %v\n", plugin.Name, err)
		}
	}
}

// buildEnv builds environment variables for a plugin execution.
func (r *Runner) buildEnv(trigger config.TriggerType, execCtx *ExecuteContext) []string {
	env := []string{
		fmt.Sprintf("LOCALDSMC_EVENT=%s", trigger),
		fmt.Sprintf("LOCALDSMC_TIMESTAMP=%s", time.Now().Format(time.RFC3339)),
	}

	if execCtx != nil {
		if execCtx.OutputFile != "" {
			env = append(env, fmt.Sprintf("LOCALDSMC_OUTPUT_FILE=%s", execCtx.OutputFile))
		}

		// Meeting-specific variables
		if trigger == config.TriggerOnMeetingStart || trigger == config.TriggerOnMeetingEnd {
			env = append(env, fmt.Sprintf("LOCALDSMC_MEETING_TYPE=%s", execCtx.MeetingType.String()))
			if execCtx.MeetingCode != "" {
				env = append(env, fmt.Sprintf("LOCALDSMC_MEETING_CODE=%s", execCtx.MeetingCode))
			}
			if execCtx.MeetingTitle != "" {
				env = append(env, fmt.Sprintf("LOCALDSMC_MEETING_TITLE=%s", execCtx.MeetingTitle))
			}
			if trigger == config.TriggerOnMeetingEnd {
				env = append(env, fmt.Sprintf("LOCALDSMC_MEETING_DURATION=%d", int(execCtx.MeetingDuration.Seconds())))
			}
		}
	}

	return env
}

// HasPlugins returns true if there are any plugins configured.
func (r *Runner) HasPlugins() bool {
	return len(r.plugins) > 0
}

// HasTrigger returns true if there are any plugins with the given trigger.
func (r *Runner) HasTrigger(trigger config.TriggerType) bool {
	for _, p := range r.plugins {
		if p.Trigger == trigger {
			return true
		}
	}
	return false
}
