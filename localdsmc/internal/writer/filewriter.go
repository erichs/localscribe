// Package writer provides file writing functionality with smart flushing.
package writer

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Options configures the file writer behavior.
type Options struct {
	FlushSize     int           // Flush after this many bytes
	FlushInterval time.Duration // Flush after this duration
	ReopenOnFlush bool          // Close and reopen file on flush (for file watchers)
}

// DefaultOptions returns sensible default options.
func DefaultOptions() Options {
	return Options{
		FlushSize:     200,
		FlushInterval: 2 * time.Second,
		ReopenOnFlush: true,
	}
}

// FileWriter writes to a file with configurable flushing behavior.
type FileWriter struct {
	path         string
	file         *os.File
	opts         Options
	mu           sync.Mutex
	bytesWritten int
	lastFlush    time.Time
}

// New creates a new FileWriter with default options.
func New(path string) (*FileWriter, error) {
	return NewWithOptions(path, DefaultOptions())
}

// NewWithOptions creates a new FileWriter with the specified options.
func NewWithOptions(path string, opts Options) (*FileWriter, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	return &FileWriter{
		path:      path,
		file:      file,
		opts:      opts,
		lastFlush: time.Now(),
	}, nil
}

// Write writes data to the file.
func (w *FileWriter) Write(data string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.file.WriteString(data)
	if err != nil {
		return err
	}
	w.bytesWritten += n

	// Check if we should flush
	if w.shouldFlush() {
		return w.flushLocked()
	}

	return nil
}

// WriteLine writes data followed by a newline.
func (w *FileWriter) WriteLine(data string) error {
	return w.Write(data + "\n")
}

// shouldFlush returns true if we should flush based on size or time.
func (w *FileWriter) shouldFlush() bool {
	if w.bytesWritten >= w.opts.FlushSize {
		return true
	}
	if time.Since(w.lastFlush) >= w.opts.FlushInterval {
		return true
	}
	return false
}

// Flush forces a flush of the file.
func (w *FileWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushLocked()
}

// flushLocked performs the actual flush (must be called with lock held).
func (w *FileWriter) flushLocked() error {
	if err := w.file.Sync(); err != nil {
		return err
	}

	if w.opts.ReopenOnFlush {
		// Close and reopen to trigger file system events
		if err := w.file.Close(); err != nil {
			return err
		}

		file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		w.file = file
	}

	w.bytesWritten = 0
	w.lastFlush = time.Now()
	return nil
}

// Close closes the file writer.
func (w *FileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Final flush
	_ = w.file.Sync()
	return w.file.Close()
}

// BytesWritten returns the number of bytes written since the last flush.
func (w *FileWriter) BytesWritten() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bytesWritten
}

// Path returns the file path.
func (w *FileWriter) Path() string {
	return w.path
}

// Writer is the interface for writing output.
type Writer interface {
	Write(data string) error
	WriteLine(data string) error
	Flush() error
	Close() error
}

// MultiWriter writes to multiple destinations simultaneously.
type MultiWriter struct {
	file   *FileWriter
	stdout io.Writer
	mu     sync.Mutex
}

// NewMultiWriter creates a writer that writes to both a file and stdout.
func NewMultiWriter(file *FileWriter, stdout io.Writer) *MultiWriter {
	return &MultiWriter{
		file:   file,
		stdout: stdout,
	}
}

// Write writes to both file and stdout.
func (m *MultiWriter) Write(data string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Write to stdout (don't fail on stdout errors)
	if m.stdout != nil {
		_, _ = m.stdout.Write([]byte(data))
	}

	// Write to file
	return m.file.Write(data)
}

// WriteLine writes a line to both file and stdout.
func (m *MultiWriter) WriteLine(data string) error {
	return m.Write(data + "\n")
}

// Flush flushes the file writer.
func (m *MultiWriter) Flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.file.Flush()
}

// Close closes the file writer.
func (m *MultiWriter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.file.Close()
}

// NullWriter discards all writes (for testing or when no output file is needed).
type NullWriter struct{}

func (n *NullWriter) Write(data string) error   { return nil }
func (n *NullWriter) WriteLine(data string) error { return nil }
func (n *NullWriter) Flush() error              { return nil }
func (n *NullWriter) Close() error              { return nil }
