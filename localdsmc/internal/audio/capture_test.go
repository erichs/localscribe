package audio

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, 24000, SampleRate)
	assert.Equal(t, 1920, ChunkSize)
	assert.Equal(t, 1, Channels)
}

func TestApplyGain(t *testing.T) {
	tests := []struct {
		name     string
		input    []float32
		gain     float64
		expected []float32
	}{
		{
			name:     "unity gain",
			input:    []float32{0.5, -0.5, 0.25},
			gain:     1.0,
			expected: []float32{0.5, -0.5, 0.25},
		},
		{
			name:     "double gain",
			input:    []float32{0.5, -0.5, 0.25},
			gain:     2.0,
			expected: []float32{1.0, -1.0, 0.5},
		},
		{
			name:     "half gain",
			input:    []float32{0.5, -0.5, 0.25},
			gain:     0.5,
			expected: []float32{0.25, -0.25, 0.125},
		},
		{
			name:     "clipping positive",
			input:    []float32{0.8},
			gain:     2.0,
			expected: []float32{1.0}, // Clipped to 1.0
		},
		{
			name:     "clipping negative",
			input:    []float32{-0.8},
			gain:     2.0,
			expected: []float32{-1.0}, // Clipped to -1.0
		},
		{
			name:     "empty input",
			input:    []float32{},
			gain:     2.0,
			expected: []float32{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ApplyGain(tt.input, tt.gain)
			assert.Equal(t, len(tt.expected), len(result))
			for i := range result {
				assert.InDelta(t, tt.expected[i], result[i], 0.0001)
			}
		})
	}
}

func TestApplyGainInPlace(t *testing.T) {
	input := []float32{0.5, -0.5, 0.25}
	ApplyGainInPlace(input, 2.0)

	assert.InDelta(t, 1.0, input[0], 0.0001)
	assert.InDelta(t, -1.0, input[1], 0.0001)
	assert.InDelta(t, 0.5, input[2], 0.0001)
}

func TestConvertStereoToMono(t *testing.T) {
	// Interleaved stereo: [L, R, L, R, ...]
	stereo := []float32{0.5, 0.3, -0.5, -0.3, 0.2, 0.4}
	mono := ConvertStereoToMono(stereo)

	assert.Equal(t, 3, len(mono))
	assert.InDelta(t, 0.4, mono[0], 0.0001) // (0.5+0.3)/2
	assert.InDelta(t, -0.4, mono[1], 0.0001) // (-0.5-0.3)/2
	assert.InDelta(t, 0.3, mono[2], 0.0001) // (0.2+0.4)/2
}

func TestConvertStereoToMonoEmpty(t *testing.T) {
	stereo := []float32{}
	mono := ConvertStereoToMono(stereo)
	assert.Equal(t, 0, len(mono))
}

func TestConvertStereoToMonoOddLength(t *testing.T) {
	// Odd length should handle gracefully
	stereo := []float32{0.5, 0.3, 0.2}
	mono := ConvertStereoToMono(stereo)
	assert.Equal(t, 1, len(mono)) // Only complete pairs
}

func TestMockCaptureSource(t *testing.T) {
	// Create a mock capture source for testing
	chunks := make(chan []float32, 10)
	mock := &MockCaptureSource{
		chunks: chunks,
	}

	// Send test data
	testChunk := []float32{0.1, 0.2, 0.3}
	chunks <- testChunk

	// Receive
	received := <-mock.Chunks()
	assert.Equal(t, testChunk, received)

	// Close
	close(chunks)
}

// MockCaptureSource implements CaptureSource for testing
type MockCaptureSource struct {
	chunks chan []float32
	closed bool
}

func (m *MockCaptureSource) Start() error {
	return nil
}

func (m *MockCaptureSource) Stop() error {
	return nil
}

func (m *MockCaptureSource) Chunks() <-chan []float32 {
	return m.chunks
}

func (m *MockCaptureSource) Close() error {
	m.closed = true
	return nil
}

func TestDeviceInfoString(t *testing.T) {
	info := DeviceInfo{
		Index:      0,
		Name:       "Test Microphone",
		SampleRate: 48000,
		Channels:   2,
		IsDefault:  true,
	}

	str := info.String()
	assert.Contains(t, str, "Test Microphone")
	assert.Contains(t, str, "48000")
	assert.Contains(t, str, "(default)")
}

func TestDeviceInfoStringNonDefault(t *testing.T) {
	info := DeviceInfo{
		Index:      1,
		Name:       "USB Mic",
		SampleRate: 44100,
		Channels:   1,
		IsDefault:  false,
	}

	str := info.String()
	assert.Contains(t, str, "USB Mic")
	assert.NotContains(t, str, "(default)")
}
