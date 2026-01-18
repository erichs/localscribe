package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

func TestAudioMessageEncode(t *testing.T) {
	msg := &AudioMessage{
		Type: "Audio",
		PCM:  []float32{0.1, -0.2, 0.3},
	}

	data, err := msgpack.Marshal(msg)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Decode back to verify
	var decoded AudioMessage
	err = msgpack.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "Audio", decoded.Type)
	assert.Equal(t, []float32{0.1, -0.2, 0.3}, decoded.PCM)
}

func TestWordMessageDecode(t *testing.T) {
	// Simulate what the server sends
	msg := map[string]interface{}{
		"type": "Word",
		"text": "hello",
	}

	data, err := msgpack.Marshal(msg)
	require.NoError(t, err)

	decoded, err := DecodeMessage(data)
	require.NoError(t, err)

	wordMsg, ok := decoded.(*WordMessage)
	require.True(t, ok, "expected *WordMessage, got %T", decoded)
	assert.Equal(t, "Word", wordMsg.Type)
	assert.Equal(t, "hello", wordMsg.Text)
}

func TestStepMessageDecode(t *testing.T) {
	// Simulate VAD message from server
	// prs has 4 prediction heads
	msg := map[string]interface{}{
		"type": "Step",
		"prs": []interface{}{
			[]interface{}{float64(0.1)},                             // 0.5s pause
			[]interface{}{float64(0.2)},                             // 1.0s pause
			[]interface{}{float64(0.85)},                            // 2.0s pause (end of turn)
			[]interface{}{float64(0.3)},                             // 3.0s pause
		},
	}

	data, err := msgpack.Marshal(msg)
	require.NoError(t, err)

	decoded, err := DecodeMessage(data)
	require.NoError(t, err)

	stepMsg, ok := decoded.(*StepMessage)
	require.True(t, ok, "expected *StepMessage, got %T", decoded)
	assert.Equal(t, "Step", stepMsg.Type)
	assert.True(t, stepMsg.IsEndOfTurn(), "expected end of turn with prs[2][0]=0.85")
}

func TestStepMessageNotEndOfTurn(t *testing.T) {
	msg := map[string]interface{}{
		"type": "Step",
		"prs": []interface{}{
			[]interface{}{float64(0.1)},
			[]interface{}{float64(0.2)},
			[]interface{}{float64(0.3)}, // Below 0.5 threshold
			[]interface{}{float64(0.1)},
		},
	}

	data, err := msgpack.Marshal(msg)
	require.NoError(t, err)

	decoded, err := DecodeMessage(data)
	require.NoError(t, err)

	stepMsg := decoded.(*StepMessage)
	assert.False(t, stepMsg.IsEndOfTurn(), "should not be end of turn with prs[2][0]=0.3")
}

func TestDecodeUnknownMessage(t *testing.T) {
	msg := map[string]interface{}{
		"type": "Unknown",
		"data": "something",
	}

	data, err := msgpack.Marshal(msg)
	require.NoError(t, err)

	decoded, err := DecodeMessage(data)
	require.NoError(t, err)

	unknown, ok := decoded.(*UnknownMessage)
	require.True(t, ok)
	assert.Equal(t, "Unknown", unknown.Type)
}

func TestDecodeInvalidMessage(t *testing.T) {
	// Invalid msgpack data
	_, err := DecodeMessage([]byte{0xFF, 0xFF, 0xFF})
	assert.Error(t, err)
}

func TestNewAudioMessage(t *testing.T) {
	pcm := []float32{0.1, 0.2, 0.3}
	msg := NewAudioMessage(pcm)

	assert.Equal(t, "Audio", msg.Type)
	assert.Equal(t, pcm, msg.PCM)
}

func TestStepMessageEmptyPrs(t *testing.T) {
	msg := &StepMessage{
		Type: "Step",
		Prs:  [][]float64{},
	}

	// Should not panic, should return false
	assert.False(t, msg.IsEndOfTurn())
}

func TestStepMessageInsufficientPrs(t *testing.T) {
	msg := &StepMessage{
		Type: "Step",
		Prs:  [][]float64{{0.1}, {0.2}}, // Only 2 heads, need at least 3
	}

	assert.False(t, msg.IsEndOfTurn())
}

func TestStepMessageEmptyThirdHead(t *testing.T) {
	msg := &StepMessage{
		Type: "Step",
		Prs:  [][]float64{{0.1}, {0.2}, {}}, // Third head is empty
	}

	assert.False(t, msg.IsEndOfTurn())
}
