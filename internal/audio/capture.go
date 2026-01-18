// Package audio provides audio capture functionality using portaudio.
package audio

import (
	"fmt"
	"sync"

	"github.com/gordonklaus/portaudio"
)

const (
	// SampleRate is the required sample rate for moshi-server (24kHz).
	SampleRate = 24000
	// ChunkSize is the number of samples per chunk (80ms at 24kHz).
	ChunkSize = 1920
	// Channels is the number of audio channels (mono).
	Channels = 1
	// ChannelBufferSize is the capacity of the chunk channel.
	ChannelBufferSize = 50
)

// CaptureSource is the interface for audio capture implementations.
type CaptureSource interface {
	Start() error
	Stop() error
	Chunks() <-chan []float32
	Close() error
}

// DeviceInfo contains information about an audio input device.
type DeviceInfo struct {
	Index      int
	Name       string
	SampleRate float64
	Channels   int
	IsDefault  bool
}

// String returns a human-readable representation of the device.
func (d DeviceInfo) String() string {
	suffix := ""
	if d.IsDefault {
		suffix = " (default)"
	}
	return fmt.Sprintf("[%d] %s - %dHz, %d ch%s",
		d.Index, d.Name, int(d.SampleRate), d.Channels, suffix)
}

// Capture handles audio capture from a microphone using portaudio.
type Capture struct {
	stream      *portaudio.Stream
	deviceIndex int
	gain        float64
	chunks      chan []float32
	mu          sync.Mutex
	running     bool
}

// NewCapture creates a new audio capture instance.
// deviceIndex of -1 means use the default input device.
func NewCapture(deviceIndex int, gain float64) (*Capture, error) {
	if err := portaudio.Initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize portaudio: %w", err)
	}

	return &Capture{
		deviceIndex: deviceIndex,
		gain:        gain,
		chunks:      make(chan []float32, ChannelBufferSize),
	}, nil
}

// Start begins audio capture.
func (c *Capture) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	// Get the input device
	var device *portaudio.DeviceInfo
	if c.deviceIndex >= 0 {
		devices, err := portaudio.Devices()
		if err != nil {
			return fmt.Errorf("failed to list devices: %w", err)
		}
		if c.deviceIndex >= len(devices) {
			return fmt.Errorf("device index %d out of range (max %d)", c.deviceIndex, len(devices)-1)
		}
		device = devices[c.deviceIndex]
	} else {
		var err error
		device, err = portaudio.DefaultInputDevice()
		if err != nil {
			return fmt.Errorf("failed to get default input device: %w", err)
		}
	}

	// Configure stream parameters
	params := portaudio.LowLatencyParameters(device, nil)
	params.Input.Channels = Channels
	params.SampleRate = SampleRate
	params.FramesPerBuffer = ChunkSize

	// Create buffer for callback
	buffer := make([]float32, ChunkSize)

	// Open stream
	stream, err := portaudio.OpenStream(params, func(in []float32) {
		// Copy and apply gain
		chunk := make([]float32, len(in))
		copy(chunk, in)
		ApplyGainInPlace(chunk, c.gain)

		// Send to channel (non-blocking)
		select {
		case c.chunks <- chunk:
		default:
			// Channel full, drop chunk (load shedding)
		}
	})
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	_ = buffer // suppress unused warning

	c.stream = stream
	c.running = true

	return stream.Start()
}

// Stop stops audio capture.
func (c *Capture) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	c.running = false
	return c.stream.Stop()
}

// Chunks returns the channel that receives audio chunks.
func (c *Capture) Chunks() <-chan []float32 {
	return c.chunks
}

// Close releases all resources.
func (c *Capture) Close() error {
	c.Stop()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stream != nil {
		c.stream.Close()
		c.stream = nil
	}

	return portaudio.Terminate()
}

// ListDevices returns a list of available input devices.
func ListDevices() ([]DeviceInfo, error) {
	if err := portaudio.Initialize(); err != nil {
		return nil, err
	}
	defer portaudio.Terminate()

	devices, err := portaudio.Devices()
	if err != nil {
		return nil, err
	}

	defaultDevice, _ := portaudio.DefaultInputDevice()
	var defaultName string
	if defaultDevice != nil {
		defaultName = defaultDevice.Name
	}

	var result []DeviceInfo
	for i, d := range devices {
		if d.MaxInputChannels > 0 {
			result = append(result, DeviceInfo{
				Index:      i,
				Name:       d.Name,
				SampleRate: d.DefaultSampleRate,
				Channels:   d.MaxInputChannels,
				IsDefault:  d.Name == defaultName,
			})
		}
	}

	return result, nil
}

// ApplyGain multiplies samples by the gain factor and clips to [-1, 1].
func ApplyGain(samples []float32, gain float64) []float32 {
	result := make([]float32, len(samples))
	g := float32(gain)

	for i, s := range samples {
		v := s * g
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		result[i] = v
	}

	return result
}

// ApplyGainInPlace applies gain to samples in place.
func ApplyGainInPlace(samples []float32, gain float64) {
	g := float32(gain)

	for i := range samples {
		v := samples[i] * g
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		samples[i] = v
	}
}

// ConvertStereoToMono converts interleaved stereo samples to mono by averaging.
func ConvertStereoToMono(stereo []float32) []float32 {
	if len(stereo) < 2 {
		return []float32{}
	}

	mono := make([]float32, len(stereo)/2)
	for i := 0; i < len(mono); i++ {
		mono[i] = (stereo[i*2] + stereo[i*2+1]) / 2
	}

	return mono
}
