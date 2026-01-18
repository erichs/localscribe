package client

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	// ASREndpoint is the path for the ASR streaming API.
	ASREndpoint = "/api/asr-streaming"
)

// Client handles WebSocket communication with moshi-server.
type Client struct {
	conn      *websocket.Conn
	serverURL string
	apiKey    string
	mu        sync.Mutex
	closed    bool
}

// Connect establishes a WebSocket connection to the moshi-server.
func Connect(serverURL, apiKey string) (*Client, error) {
	url := buildURL(serverURL)

	header := make(http.Header)
	header.Set("kyutai-api-key", apiKey)

	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		return nil, err
	}

	return &Client{
		conn:      conn,
		serverURL: serverURL,
		apiKey:    apiKey,
		closed:    false,
	}, nil
}

// Reconnect attempts to reconnect with exponential backoff.
// Returns nil on successful reconnection, error if all attempts fail or context is cancelled.
// maxAttempts of 0 means unlimited attempts.
func (c *Client) Reconnect(maxAttempts int, onAttempt func(attempt int, delay time.Duration)) error {
	c.mu.Lock()
	// Close existing connection if any
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.closed = false
	c.mu.Unlock()

	delay := time.Second
	maxDelay := 60 * time.Second
	attempt := 0

	for {
		attempt++
		if maxAttempts > 0 && attempt > maxAttempts {
			return websocket.ErrCloseSent
		}

		if onAttempt != nil {
			onAttempt(attempt, delay)
		}

		time.Sleep(delay)

		url := buildURL(c.serverURL)
		header := make(http.Header)
		header.Set("kyutai-api-key", c.apiKey)

		conn, _, err := websocket.DefaultDialer.Dial(url, header)
		if err == nil {
			c.mu.Lock()
			c.conn = conn
			c.closed = false
			c.mu.Unlock()
			return nil
		}

		// Exponential backoff
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

// buildURL ensures the URL has the correct ASR endpoint path.
func buildURL(serverURL string) string {
	// Remove trailing slash
	serverURL = strings.TrimSuffix(serverURL, "/")

	// If already has the endpoint, return as-is
	if strings.HasSuffix(serverURL, ASREndpoint) {
		return serverURL
	}

	return serverURL + ASREndpoint
}

// SendAudio sends PCM audio samples to the server.
func (c *Client) SendAudio(pcm []float32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return websocket.ErrCloseSent
	}

	data, err := EncodeAudioMessage(pcm)
	if err != nil {
		return err
	}

	return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// Receive waits for and decodes the next message from the server.
func (c *Client) Receive() (Message, error) {
	_, data, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	return DecodeMessage(data)
}

// ReceiveRaw returns the raw bytes of the next message.
func (c *Client) ReceiveRaw() ([]byte, error) {
	_, data, err := c.conn.ReadMessage()
	return data, err
}

// Close closes the WebSocket connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	// Send close message
	_ = c.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)

	return c.conn.Close()
}

// IsClosed returns true if the connection has been closed.
func (c *Client) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// SendRaw sends raw msgpack bytes to the server.
func (c *Client) SendRaw(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return websocket.ErrCloseSent
	}

	return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

// SendMessage sends any msgpack-serializable message to the server.
func (c *Client) SendMessage(msg interface{}) error {
	data, err := msgpack.Marshal(msg)
	if err != nil {
		return err
	}
	return c.SendRaw(data)
}
