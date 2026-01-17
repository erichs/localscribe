# Go Client Specification for Kyutai STT

This document specifies a Go client for the existing `moshi-server` WebSocket API.

## Prerequisites

### Install moshi-server

```bash
# macOS (Apple Silicon)
cargo install --features metal moshi-server

# Linux/Windows (NVIDIA GPU)
cargo install --features cuda moshi-server
```

### Run moshi-server

```bash
# From the delayed-streams-modeling repo root
moshi-server worker --config configs/config-stt-en_fr-hf.toml
```

The server will:
- Download model files on first run (~2GB)
- Listen on `ws://127.0.0.1:8080/api/asr-streaming`
- Accept connections with API key `public_token`

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                    Go Client (stt-rs/golang-client)                 │
│                                                                     │
│  ┌──────────────┐   ┌──────────────┐   ┌────────────────────────┐  │
│  │ Microphone   │──▶│ Audio        │──▶│ WebSocket Client       │  │
│  │ Capture      │   │ Resampling   │   │ (msgpack encoding)     │  │
│  └──────────────┘   └──────────────┘   └───────────┬────────────┘  │
│                                                    │               │
│  ┌──────────────┐   ┌──────────────┐               │               │
│  │ File Logger  │◀──│ Post-        │◀──────────────┘               │
│  │ (append)     │   │ Processor    │   (receives transcripts)     │
│  └──────────────┘   └──────────────┘                               │
└─────────────────────────────────────────────────────────────────────┘
                              │
                              │ WebSocket (msgpack)
                              ▼
┌─────────────────────────────────────────────────────────────────────┐
│                 moshi-server (existing, no changes)                 │
│                                                                     │
│  Endpoint: ws://127.0.0.1:8080/api/asr-streaming                   │
│  Auth: Header "kyutai-api-key: public_token"                       │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Part 1: Protocol (Existing moshi-server API)

### Connection

```
URL: ws://127.0.0.1:8080/api/asr-streaming
Header: kyutai-api-key: public_token
```

Alternative auth via query param: `ws://127.0.0.1:8080/api/asr-streaming?auth_id=public_token`

### Audio Format

| Parameter | Value |
|-----------|-------|
| Sample rate | 24,000 Hz |
| Chunk size | 1,920 samples |
| Chunk duration | 80 ms |
| Format | mono float32 |
| Range | [-1.0, 1.0] |

### Messages (msgpack encoded)

**Client → Server: Audio chunk**
```go
map[string]interface{}{
    "type": "Audio",
    "pcm":  []float32{0.1, -0.2, ...},  // 1920 samples
}
```

**Server → Client: Transcribed word**
```go
map[string]interface{}{
    "type": "Word",
    "text": "hello",
}
```

**Server → Client: VAD step**
```go
map[string]interface{}{
    "type": "Step",
    "prs":  [][]float32{...},  // VAD probabilities
}
```

VAD `prs` contains 4 prediction heads for pause detection at different thresholds:
- Index 0: 0.5 second pause
- Index 1: 1.0 second pause
- Index 2: 2.0 second pause (recommended, used by Unmute)
- Index 3: 3.0 second pause

When `prs[2][0] > 0.5`, end-of-turn is detected.

---

## Part 2: Go Client Implementation

### Project Structure

```
stt-rs/golang-client/
├── cmd/
│   └── stt-client/
│       └── main.go           # CLI entry point
├── internal/
│   ├── audio/
│   │   ├── capture.go        # Microphone capture (portaudio)
│   │   └── resample.go       # Resampling to 24kHz
│   ├── client/
│   │   ├── websocket.go      # WebSocket client
│   │   └── messages.go       # msgpack message types
│   ├── processor/
│   │   ├── postprocess.go    # Line break insertion
│   │   └── vad.go            # VAD-based pause/resume
│   └── writer/
│       └── filewriter.go     # Append-only file logger
├── go.mod
└── go.sum
```

### CLI Interface

```
stt-client [OPTIONS]

Options:
    --server <URL>          Server WebSocket URL (default: ws://127.0.0.1:8080)
    --api-key <KEY>         API key (default: public_token)
    --device <INDEX>        Audio input device index
    --list-devices          List available audio devices
    --output <FILE>         Output transcript file (append mode)
    --gain <FLOAT>          Audio gain multiplier (default: 1.0)
    --vad-pause             Pause transcription on VAD end-of-turn detection
    --pause-threshold <SEC> Silence duration to trigger line break (default: 2.0)
    --debug                 Enable debug output
```

### Go Dependencies

```go
// go.mod
module github.com/user/stt-client

go 1.21

require (
    github.com/gorilla/websocket v1.5.1      // WebSocket client
    github.com/vmihailenco/msgpack/v5 v5.4.1 // msgpack encoding
    github.com/gordonklaus/portaudio v0.0.0  // Audio capture
)
```

---

## Part 3: File Logger Specification

The file logger MUST:
- Always open files in append mode
- Never truncate existing content
- Flush after each complete sentence/phrase
- Re-open file on each flush (for file watcher compatibility)
- Handle concurrent writes safely

```go
type FileLogger struct {
    path          string
    file          *os.File
    mu            sync.Mutex
    bytesWritten  int
    lastFlush     time.Time
    flushSize     int           // Flush after N bytes (default: 200)
    flushInterval time.Duration // Flush after duration (default: 2s)
}

func (l *FileLogger) Write(text string) error
func (l *FileLogger) Flush() error
func (l *FileLogger) Close() error
```

---

## Part 4: VAD/Transcription Pause/Resume

The client manages three states:

```go
type TranscriptionState int

const (
    StateActive   TranscriptionState = iota  // Receiving and processing
    StatePaused                              // User-initiated pause
    StateVADPause                            // Auto-paused on end-of-turn
)
```

**Pause Triggers:**
1. **User-initiated**: Keyboard shortcut or signal (SIGUSR1)
2. **VAD end-of-turn**: When `--vad-pause` enabled and server sends VAD with `prs[2][0] > 0.5`

**Resume Triggers:**
1. **User-initiated**: Keyboard shortcut or signal (SIGUSR2)
2. **VAD speech detection**: Auto-resume when speech detected after VAD pause

**Pause Behavior:**
- Audio capture continues (to detect speech resume)
- Audio frames are NOT sent to server
- File logger flushes current buffer

**Resume Behavior:**
- Resume sending audio frames to server
- Insert line break in transcript

---

## Part 5: Post-Processing - Sensible Line Breaks

The post-processor inserts line breaks based on:

### Rules (in priority order):

1. **VAD End-of-Turn**: Insert `\n\n` (paragraph break) when VAD detects end-of-turn
2. **Sentence Boundaries**: Insert `\n` after sentence-ending punctuation (`.`, `!`, `?`) followed by capitalized word
3. **Long Silence**: Insert `\n` after configurable silence threshold (default: 2 seconds)
4. **Character Limit**: Soft wrap at ~80 characters on word boundaries (for log readability)

### Implementation:

```go
type PostProcessor struct {
    lastWordTime    time.Time
    currentLine     strings.Builder
    pauseThreshold  time.Duration
    maxLineLength   int
}

func (p *PostProcessor) Process(msg Message) string {
    switch msg.Type {
    case "Word":
        return p.processWord(msg.Text)
    case "Step":
        if msg.IsEndOfTurn() {
            return p.insertParagraphBreak()
        }
    }
    return ""
}

func (p *PostProcessor) processWord(text string) string {
    var result strings.Builder

    // Check for long silence since last word
    if !p.lastWordTime.IsZero() {
        elapsed := time.Since(p.lastWordTime)
        if elapsed > p.pauseThreshold {
            result.WriteString("\n")
            p.currentLine.Reset()
        }
    }
    p.lastWordTime = time.Now()

    // Check for sentence boundary
    if p.endsWithSentence() && startsWithCapital(text) {
        result.WriteString("\n")
        p.currentLine.Reset()
    }

    // Check line length
    if p.currentLine.Len() > p.maxLineLength {
        result.WriteString("\n")
        p.currentLine.Reset()
    }

    // Add the word
    if p.currentLine.Len() > 0 {
        result.WriteString(" ")
    }
    result.WriteString(text)
    p.currentLine.WriteString(" " + text)

    return result.String()
}
```

### Example Output:

```
Hello how are you doing today. I wanted to talk about the project.

We need to focus on three main areas. First the backend API changes.
Second the frontend updates. And third the deployment pipeline.

That sounds like a good plan. Let me know if you need any help.
```

---

## Part 6: Message Sequence

```
Go Client                                 moshi-server
    │                                         │
    │──── WS Connect ─────────────────────────▶
    │     Header: kyutai-api-key: public_token│
    │                                         │
    │──── Audio msgpack ──────────────────────▶
    │     {type: "Audio", pcm: [...]}         │
    │                                         │
    │──── Audio msgpack ──────────────────────▶
    │                                         │
    │◀──── Word msgpack ──────────────────────│
    │      {type: "Word", text: "hello"}      │
    │                                         │
    │──── Audio msgpack ──────────────────────▶
    │                                         │
    │◀──── Word msgpack ──────────────────────│
    │      {type: "Word", text: "world"}      │
    │                                         │
    │◀──── Step msgpack ──────────────────────│
    │      {type: "Step", prs: [[0.8],...]}   │
    │      (end-of-turn detected)             │
    │                                         │
    │     [Client pauses if --vad-pause]      │
    │     [Client inserts paragraph break]    │
    │                                         │
    │──── Audio msgpack ──────────────────────▶
    │         ...                             │
```

---

## Part 7: Implementation Phases

### Phase 1: MVP
1. WebSocket connection to moshi-server
2. Microphone capture at 24kHz (or resample)
3. msgpack encode/decode
4. Print transcripts to stdout

### Phase 2: File Logging
1. Append-only file writer
2. Smart flushing for file watchers

### Phase 3: Post-Processing
1. VAD-based paragraph breaks
2. Sentence boundary detection
3. Silence-based line breaks

### Phase 4: Polish
1. Pause/resume controls (SIGUSR1/SIGUSR2)
2. Device selection
3. Gain control
4. Reconnection logic

---

## Appendix A: Audio Capture Example

```go
package audio

import (
    "github.com/gordonklaus/portaudio"
)

const (
    SampleRate = 24000
    ChunkSize  = 1920  // 80ms at 24kHz
)

type Capture struct {
    stream *portaudio.Stream
    output chan []float32
}

func NewCapture(deviceIndex int) (*Capture, error) {
    if err := portaudio.Initialize(); err != nil {
        return nil, err
    }

    c := &Capture{
        output: make(chan []float32, 50),
    }

    params := portaudio.LowLatencyParameters(nil, nil)
    params.Input.Channels = 1
    params.SampleRate = SampleRate
    params.FramesPerBuffer = ChunkSize

    if deviceIndex >= 0 {
        devices, _ := portaudio.Devices()
        if deviceIndex < len(devices) {
            params.Input.Device = devices[deviceIndex]
        }
    }

    stream, err := portaudio.OpenStream(params, c.callback)
    if err != nil {
        return nil, err
    }
    c.stream = stream

    return c, nil
}

func (c *Capture) callback(in []float32) {
    chunk := make([]float32, len(in))
    copy(chunk, in)
    select {
    case c.output <- chunk:
    default:
        // Drop if channel full (load shedding)
    }
}

func (c *Capture) Start() error {
    return c.stream.Start()
}

func (c *Capture) Stop() error {
    return c.stream.Stop()
}

func (c *Capture) Chunks() <-chan []float32 {
    return c.output
}

func (c *Capture) Close() {
    c.stream.Close()
    portaudio.Terminate()
}
```

## Appendix B: WebSocket Client Example

```go
package client

import (
    "github.com/gorilla/websocket"
    "github.com/vmihailenco/msgpack/v5"
)

type Client struct {
    conn *websocket.Conn
}

type AudioMessage struct {
    Type string    `msgpack:"type"`
    PCM  []float32 `msgpack:"pcm"`
}

type WordMessage struct {
    Type string `msgpack:"type"`
    Text string `msgpack:"text"`
}

type StepMessage struct {
    Type string      `msgpack:"type"`
    Prs  [][]float32 `msgpack:"prs"`
}

func Connect(serverURL, apiKey string) (*Client, error) {
    header := make(http.Header)
    header.Set("kyutai-api-key", apiKey)

    conn, _, err := websocket.DefaultDialer.Dial(
        serverURL+"/api/asr-streaming",
        header,
    )
    if err != nil {
        return nil, err
    }

    return &Client{conn: conn}, nil
}

func (c *Client) SendAudio(pcm []float32) error {
    msg := AudioMessage{Type: "Audio", PCM: pcm}
    data, err := msgpack.Marshal(msg)
    if err != nil {
        return err
    }
    return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (c *Client) Receive() (interface{}, error) {
    _, data, err := c.conn.ReadMessage()
    if err != nil {
        return nil, err
    }

    var raw map[string]interface{}
    if err := msgpack.Unmarshal(data, &raw); err != nil {
        return nil, err
    }

    switch raw["type"] {
    case "Word":
        var msg WordMessage
        msgpack.Unmarshal(data, &msg)
        return msg, nil
    case "Step":
        var msg StepMessage
        msgpack.Unmarshal(data, &msg)
        return msg, nil
    }

    return raw, nil
}

func (c *Client) Close() error {
    return c.conn.Close()
}
```
