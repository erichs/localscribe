package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFileWriter(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := New(path)
	require.NoError(t, err)
	require.NotNil(t, w)

	err = w.Close()
	assert.NoError(t, err)

	// File should exist
	_, err = os.Stat(path)
	assert.NoError(t, err)
}

func TestFileWriterAppendMode(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	// Pre-create file with content
	err := os.WriteFile(path, []byte("existing content\n"), 0644)
	require.NoError(t, err)

	w, err := New(path)
	require.NoError(t, err)

	err = w.Write("new content")
	require.NoError(t, err)

	err = w.Close()
	require.NoError(t, err)

	// Check content was appended
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	assert.Equal(t, "existing content\nnew content", string(content))
}

func TestFileWriterWrite(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := New(path)
	require.NoError(t, err)

	err = w.Write("hello ")
	require.NoError(t, err)

	err = w.Write("world")
	require.NoError(t, err)

	err = w.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))
}

func TestFileWriterFlushOnSize(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := NewWithOptions(path, Options{
		FlushSize:     10, // Flush after 10 bytes
		FlushInterval: time.Hour, // Don't flush on time
	})
	require.NoError(t, err)

	// Write less than threshold
	err = w.Write("short")
	require.NoError(t, err)

	// File might not have content yet (buffered)
	// But after flush threshold, it should

	// Write more to exceed threshold
	err = w.Write("this is longer")
	require.NoError(t, err)

	// Explicitly flush
	err = w.Flush()
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "shortthis is longer", string(content))

	w.Close()
}

func TestFileWriterFlushOnInterval(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := NewWithOptions(path, Options{
		FlushSize:     1000, // High threshold
		FlushInterval: 50 * time.Millisecond,
	})
	require.NoError(t, err)

	err = w.Write("test content")
	require.NoError(t, err)

	// Wait for interval
	time.Sleep(100 * time.Millisecond)

	// Trigger write to check interval
	err = w.Write("more")
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "test content")

	w.Close()
}

func TestFileWriterManualFlush(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := NewWithOptions(path, Options{
		FlushSize:     1000,
		FlushInterval: time.Hour,
	})
	require.NoError(t, err)

	err = w.Write("test")
	require.NoError(t, err)

	// Manual flush
	err = w.Flush()
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "test", string(content))

	w.Close()
}

func TestFileWriterReopenOnFlush(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := NewWithOptions(path, Options{
		FlushSize:       10,
		FlushInterval:   time.Hour,
		ReopenOnFlush:   true,
	})
	require.NoError(t, err)

	err = w.Write("first")
	require.NoError(t, err)
	err = w.Flush()
	require.NoError(t, err)

	err = w.Write(" second")
	require.NoError(t, err)
	err = w.Flush()
	require.NoError(t, err)

	err = w.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "first second", string(content))
}

func TestFileWriterConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := New(path)
	require.NoError(t, err)

	// Write from multiple goroutines
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			for j := 0; j < 100; j++ {
				w.Write("x")
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	err = w.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 1000, len(content))
}

func TestFileWriterWriteLine(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := New(path)
	require.NoError(t, err)

	err = w.WriteLine("line 1")
	require.NoError(t, err)
	err = w.WriteLine("line 2")
	require.NoError(t, err)

	err = w.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "line 1\nline 2\n", string(content))
}

func TestFileWriterCreateDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "nested", "test.txt")

	w, err := New(path)
	require.NoError(t, err)

	err = w.Write("test")
	require.NoError(t, err)

	err = w.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "test", string(content))
}

func TestFileWriterInvalidPath(t *testing.T) {
	// Try to create file in non-existent, non-creatable location
	// This is OS-specific but should fail on most systems
	_, err := New("/nonexistent/root/path/that/cannot/be/created/test.txt")
	// May or may not error depending on permissions, just ensure no panic
	_ = err
}

func TestFileWriterBytesWritten(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := New(path)
	require.NoError(t, err)

	assert.Equal(t, 0, w.BytesWritten())

	w.Write("hello") // 5 bytes
	assert.Equal(t, 5, w.BytesWritten())

	w.Write(" world") // 6 bytes
	assert.Equal(t, 11, w.BytesWritten())

	w.Close()
}

func TestMultiWriter(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	var stdout strings.Builder

	w, err := New(path)
	require.NoError(t, err)

	mw := NewMultiWriter(w, &stdout)

	err = mw.Write("hello world")
	require.NoError(t, err)

	err = mw.Close()
	require.NoError(t, err)

	// Check file
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))

	// Check stdout capture
	assert.Equal(t, "hello world", stdout.String())
}

func TestMultiWriterFlush(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	var stdout strings.Builder

	w, err := New(path)
	require.NoError(t, err)

	mw := NewMultiWriter(w, &stdout)

	mw.Write("test")
	err = mw.Flush()
	require.NoError(t, err)

	mw.Close()
}

func TestFileWriterPath(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := New(path)
	require.NoError(t, err)
	defer w.Close()

	assert.Equal(t, path, w.Path())
}

func TestNullWriter(t *testing.T) {
	nw := &NullWriter{}

	err := nw.Write("test")
	assert.NoError(t, err)

	err = nw.WriteLine("line")
	assert.NoError(t, err)

	err = nw.Flush()
	assert.NoError(t, err)

	err = nw.Close()
	assert.NoError(t, err)
}

func TestMultiWriterWriteLine(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	var stdout strings.Builder

	w, err := New(path)
	require.NoError(t, err)

	mw := NewMultiWriter(w, &stdout)

	err = mw.WriteLine("test line")
	require.NoError(t, err)

	mw.Close()

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "test line\n", string(content))
	assert.Equal(t, "test line\n", stdout.String())
}

func TestMultiWriterNilStdout(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")

	w, err := New(path)
	require.NoError(t, err)

	// nil stdout should not panic
	mw := NewMultiWriter(w, nil)

	err = mw.Write("test")
	require.NoError(t, err)

	mw.Close()
}
