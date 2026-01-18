package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func TestClientConnect(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the API key header
		apiKey := r.Header.Get("kyutai-api-key")
		assert.Equal(t, "test_key", apiKey)

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer conn.Close()

		// Keep connection open briefly
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := Connect(wsURL, "test_key")
	require.NoError(t, err)
	require.NotNil(t, client)

	err = client.Close()
	assert.NoError(t, err)
}

func TestClientSendAudio(t *testing.T) {
	var receivedPCM []float32
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read one message
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg AudioMessage
		if err := msgpack.Unmarshal(data, &msg); err != nil {
			return
		}

		mu.Lock()
		receivedPCM = msg.PCM
		mu.Unlock()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := Connect(wsURL, "public_token")
	require.NoError(t, err)
	defer client.Close()

	// Send audio
	testPCM := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	err = client.SendAudio(testPCM)
	require.NoError(t, err)

	// Give server time to process
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, testPCM, receivedPCM)
}

func TestClientReceiveWord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send a Word message
		msg := map[string]interface{}{
			"type": "Word",
			"text": "hello",
		}
		data, _ := msgpack.Marshal(msg)
		conn.WriteMessage(websocket.BinaryMessage, data)

		// Keep connection open
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := Connect(wsURL, "public_token")
	require.NoError(t, err)
	defer client.Close()

	// Receive message
	msg, err := client.Receive()
	require.NoError(t, err)

	wordMsg, ok := msg.(*WordMessage)
	require.True(t, ok)
	assert.Equal(t, "hello", wordMsg.Text)
}

func TestClientReceiveStep(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send a Step message with end-of-turn
		msg := map[string]interface{}{
			"type": "Step",
			"prs": []interface{}{
				[]interface{}{0.1},
				[]interface{}{0.2},
				[]interface{}{0.8}, // End of turn
				[]interface{}{0.3},
			},
		}
		data, _ := msgpack.Marshal(msg)
		conn.WriteMessage(websocket.BinaryMessage, data)

		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := Connect(wsURL, "public_token")
	require.NoError(t, err)
	defer client.Close()

	msg, err := client.Receive()
	require.NoError(t, err)

	stepMsg, ok := msg.(*StepMessage)
	require.True(t, ok)
	assert.True(t, stepMsg.IsEndOfTurn())
}

func TestClientReceiveMultipleMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send multiple messages
		words := []string{"hello", "world", "test"}
		for _, word := range words {
			msg := map[string]interface{}{
				"type": "Word",
				"text": word,
			}
			data, _ := msgpack.Marshal(msg)
			conn.WriteMessage(websocket.BinaryMessage, data)
		}

		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := Connect(wsURL, "public_token")
	require.NoError(t, err)
	defer client.Close()

	received := []string{}
	for i := 0; i < 3; i++ {
		msg, err := client.Receive()
		require.NoError(t, err)
		wordMsg := msg.(*WordMessage)
		received = append(received, wordMsg.Text)
	}

	assert.Equal(t, []string{"hello", "world", "test"}, received)
}

func TestClientConnectFailure(t *testing.T) {
	// Try to connect to a non-existent server
	_, err := Connect("ws://localhost:59999", "key")
	assert.Error(t, err)
}

func TestClientSendAfterClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := Connect(wsURL, "public_token")
	require.NoError(t, err)

	client.Close()

	// Should fail to send after close
	err = client.SendAudio([]float32{0.1})
	assert.Error(t, err)
}

func TestBuildURL(t *testing.T) {
	tests := []struct {
		base     string
		expected string
	}{
		{"ws://localhost:8080", "ws://localhost:8080/api/asr-streaming"},
		{"ws://localhost:8080/", "ws://localhost:8080/api/asr-streaming"},
		{"wss://example.com", "wss://example.com/api/asr-streaming"},
		{"ws://localhost:8080/api/asr-streaming", "ws://localhost:8080/api/asr-streaming"},
	}

	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			result := buildURL(tt.base)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClientIsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := Connect(wsURL, "public_token")
	require.NoError(t, err)

	assert.False(t, client.IsClosed())

	client.Close()

	assert.True(t, client.IsClosed())
}

func TestClientSendMessage(t *testing.T) {
	var receivedType string
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg map[string]interface{}
		if err := msgpack.Unmarshal(data, &msg); err != nil {
			return
		}

		mu.Lock()
		receivedType, _ = msg["type"].(string)
		mu.Unlock()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := Connect(wsURL, "public_token")
	require.NoError(t, err)
	defer client.Close()

	// Send a custom message
	err = client.SendMessage(map[string]interface{}{
		"type": "Custom",
		"data": "test",
	})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "Custom", receivedType)
}

func TestClientReceiveRaw(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		msg := map[string]interface{}{"type": "Raw", "data": "test"}
		data, _ := msgpack.Marshal(msg)
		conn.WriteMessage(websocket.BinaryMessage, data)

		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := Connect(wsURL, "public_token")
	require.NoError(t, err)
	defer client.Close()

	data, err := client.ReceiveRaw()
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestClientDoubleClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := Connect(wsURL, "public_token")
	require.NoError(t, err)

	// First close
	err = client.Close()
	assert.NoError(t, err)

	// Second close should not error
	err = client.Close()
	assert.NoError(t, err)
}
