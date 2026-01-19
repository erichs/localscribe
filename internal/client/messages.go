// Package client provides WebSocket client functionality for moshi-server.
package client

import (
	"github.com/vmihailenco/msgpack/v5"
)

// Message is the interface for all message types.
type Message interface {
	MessageType() string
}

// AudioMessage is sent from client to server with audio data.
type AudioMessage struct {
	Type string    `msgpack:"type"`
	PCM  []float32 `msgpack:"pcm"`
}

// MessageType returns the message type identifier.
func (m *AudioMessage) MessageType() string { return m.Type }

// NewAudioMessage creates a new audio message with the given PCM samples.
func NewAudioMessage(pcm []float32) *AudioMessage {
	return &AudioMessage{
		Type: "Audio",
		PCM:  pcm,
	}
}

// WordMessage is received from server with transcribed text.
type WordMessage struct {
	Type string `msgpack:"type"`
	Text string `msgpack:"text"`
}

// MessageType returns the message type identifier.
func (m *WordMessage) MessageType() string { return m.Type }

// StepMessage is received from server with VAD information.
type StepMessage struct {
	Type string      `msgpack:"type"`
	Prs  [][]float64 `msgpack:"prs"`
}

// MessageType returns the message type identifier.
func (m *StepMessage) MessageType() string { return m.Type }

// IsEndOfTurn returns true if the VAD indicates end of turn.
// Uses the 2.0 second pause prediction head (index 2) with threshold 0.5.
func (m *StepMessage) IsEndOfTurn() bool {
	// Need at least 3 prediction heads
	if len(m.Prs) < 3 {
		return false
	}
	// Third head (index 2) must have at least one value
	if len(m.Prs[2]) == 0 {
		return false
	}
	// Check if probability exceeds threshold
	return m.Prs[2][0] > 0.5
}

// IsSpeechPresent returns true if VAD thinks speech is present.
// Uses Prs[0][0] < 0.4 as the threshold (lower values = more confident speech).
func (m *StepMessage) IsSpeechPresent() bool {
	if len(m.Prs) == 0 {
		return false
	}
	if len(m.Prs[0]) == 0 {
		return false
	}
	return m.Prs[0][0] < 0.4
}

// EndWordMessage is received from server to mark word boundary timing.
type EndWordMessage struct {
	Type     string  `msgpack:"type"`
	StopTime float64 `msgpack:"stop_time"`
}

// MessageType returns the message type identifier.
func (m *EndWordMessage) MessageType() string { return m.Type }

// ReadyMessage is received from server when connection is ready.
type ReadyMessage struct {
	Type string `msgpack:"type"`
}

// MessageType returns the message type identifier.
func (m *ReadyMessage) MessageType() string { return m.Type }

// ErrorMessage is received from server when an error occurs.
type ErrorMessage struct {
	Type    string `msgpack:"type"`
	Message string `msgpack:"message"`
}

// MessageType returns the message type identifier.
func (m *ErrorMessage) MessageType() string { return m.Type }

// MarkerMessage is received from server as sync acknowledgment.
type MarkerMessage struct {
	Type string `msgpack:"type"`
	ID   int64  `msgpack:"id"`
}

// MessageType returns the message type identifier.
func (m *MarkerMessage) MessageType() string { return m.Type }

// UnknownMessage represents an unrecognized message type.
type UnknownMessage struct {
	Type string
	Raw  map[string]interface{}
}

// MessageType returns the message type identifier.
func (m *UnknownMessage) MessageType() string { return m.Type }

// DecodeMessage decodes a msgpack message and returns the appropriate type.
func DecodeMessage(data []byte) (Message, error) {
	// First decode to a map to get the type
	var raw map[string]interface{}
	if err := msgpack.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	msgType, _ := raw["type"].(string)

	switch msgType {
	case "Word":
		text, _ := raw["text"].(string)
		return &WordMessage{
			Type: msgType,
			Text: text,
		}, nil

	case "Step":
		prs := decodeNestedFloat64Array(raw["prs"])
		return &StepMessage{
			Type: msgType,
			Prs:  prs,
		}, nil

	case "EndWord":
		stopTime, _ := raw["stop_time"].(float64)
		return &EndWordMessage{
			Type:     msgType,
			StopTime: stopTime,
		}, nil

	case "Ready":
		return &ReadyMessage{
			Type: msgType,
		}, nil

	case "Error":
		message, _ := raw["message"].(string)
		return &ErrorMessage{
			Type:    msgType,
			Message: message,
		}, nil

	case "Marker":
		id, _ := raw["id"].(int64)
		return &MarkerMessage{
			Type: msgType,
			ID:   id,
		}, nil

	default:
		return &UnknownMessage{
			Type: msgType,
			Raw:  raw,
		}, nil
	}
}

// decodeNestedFloat64Array converts interface{} to [][]float64.
// Handles the various ways msgpack might decode nested arrays.
func decodeNestedFloat64Array(v interface{}) [][]float64 {
	if v == nil {
		return nil
	}

	outer, ok := v.([]interface{})
	if !ok {
		return nil
	}

	result := make([][]float64, len(outer))
	for i, inner := range outer {
		switch arr := inner.(type) {
		case []interface{}:
			result[i] = make([]float64, len(arr))
			for j, val := range arr {
				switch n := val.(type) {
				case float64:
					result[i][j] = n
				case float32:
					result[i][j] = float64(n)
				case int64:
					result[i][j] = float64(n)
				case int:
					result[i][j] = float64(n)
				}
			}
		case []float64:
			result[i] = arr
		case []float32:
			result[i] = make([]float64, len(arr))
			for j, val := range arr {
				result[i][j] = float64(val)
			}
		}
	}

	return result
}

// EncodeAudioMessage encodes an audio message to msgpack bytes.
func EncodeAudioMessage(pcm []float32) ([]byte, error) {
	msg := NewAudioMessage(pcm)
	return msgpack.Marshal(msg)
}
