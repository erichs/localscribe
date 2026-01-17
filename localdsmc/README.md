# Go Client for Kyutai STT

A Go client for the Kyutai Speech-to-Text server (`moshi-server`).

## Prerequisites

### 1. Install moshi-server

The `moshi-server` crate requires Python 3.9. To keep the dependency isolated, we use `uv` to manage a self-contained Python installation.

#### Install uv (if not already installed)

```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
```

#### Install Python 3.9 via uv

```bash
uv python install 3.9
```

This installs Python to `~/.local/share/uv/python/` without affecting your system Python.

#### Build moshi-server with the isolated Python

```bash
# macOS (Apple Silicon)
PYO3_PYTHON="$(uv python find 3.9)" cargo install --features metal moshi-server --force

# macOS (Intel)
PYO3_PYTHON="$(uv python find 3.9)" cargo install --features metal moshi-server --force

# Linux (NVIDIA GPU)
PYO3_PYTHON="$(uv python find 3.9)" cargo install --features cuda moshi-server --force
```

#### Create a wrapper script

The server needs to find the Python library at runtime. Create a wrapper script:

```bash
mkdir -p ~/bin

cat > ~/bin/moshi-server-run << 'EOF'
#!/bin/bash
UV_PYTHON_DIR="$HOME/.local/share/uv/python/cpython-3.9.25-macos-aarch64-none"
export DYLD_LIBRARY_PATH="$UV_PYTHON_DIR/lib"
exec ~/.cargo/bin/moshi-server "$@"
EOF

chmod +x ~/bin/moshi-server-run
```

> **Note:** Adjust `cpython-3.9.25-macos-aarch64-none` if uv installed a different version. Check with: `ls ~/.local/share/uv/python/`

### 2. Run moshi-server


```bash
# Single-client config (recommended for personal use)
~/bin/moshi-server-run worker --config ./config-stt-en_fr-single.toml

# Multi-client config (batch_size=64, higher latency for single client)
# From the `delayed-streams-modeling` repository root:
~/bin/moshi-server-run worker --config configs/config-stt-en_fr-hf.toml
```

> **Performance Note:** Use `config-stt-en_fr-single.toml` (batch_size=1) for personal use. The `*-hf.toml` configs use batch_size=64 for serving multiple concurrent clients, which adds latency for single-client scenarios.

On first run, the server downloads model files (~2GB) from HuggingFace.

Once running, you'll see:
```
INFO listening on http://0.0.0.0:8080
INFO starting asr loop 1
```

The WebSocket endpoint is available at: `ws://127.0.0.1:8080/api/asr-streaming`

## Cleanup

To remove all components:

```bash
# Remove isolated Python 3.9
uv python uninstall 3.9

# Remove moshi-server binary
cargo uninstall moshi-server

# Remove wrapper script
rm ~/bin/moshi-server-run
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Go Client                                   │
│                                                                     │
│  ┌──────────────┐   ┌──────────────┐   ┌────────────────────────┐   │
│  │ Microphone   │──▶│ Audio        │──▶│ WebSocket Client       │   │
│  │ Capture      │   │ Resampling   │   │ (msgpack encoding)     │   │
│  └──────────────┘   └──────────────┘   └───────────┬────────────┘   │
│                                                    │                │
│  ┌──────────────┐   ┌──────────────┐               │                │
│  │ File Logger  │◀──│ Post-        │◀──────────────┘                │
│  │ (append)     │   │ Processor    │   (receives transcripts)       │
│  └──────────────┘   └──────────────┘                                │
└─────────────────────────────────────────────────────────────────────┘
                              │
                              │ WebSocket (msgpack)
                              ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         moshi-server                                │
│                                                                     │
│  Endpoint: ws://127.0.0.1:8080/api/asr-streaming                    │
│  Auth: Header "kyutai-api-key: public_token"                        │
└─────────────────────────────────────────────────────────────────────┘
```

## Protocol

See [CLIENT-SERVER-SPEC.md](CLIENT-SERVER-SPEC.md) for the full protocol specification.

### Quick Reference

**Audio Format:**
- Sample rate: 24,000 Hz
- Chunk size: 1,920 samples (80ms)
- Format: mono float32 [-1.0, 1.0]

**Send audio (msgpack):**
```json
{"type": "Audio", "pcm": [0.1, -0.2, ...]}
```

**Receive transcript (msgpack):**
```json
{"type": "Word", "text": "hello"}
```

**Receive VAD (msgpack):**
```json
{"type": "Step", "prs": [[...], [...], [...], [...]]}
```

VAD end-of-turn detected when `prs[2][0] > 0.5`.

## Building the Client

```bash
cd path/to/localdsmc
go build -o localdsmc ./cmd/localdsmc
```

## Configuration

Create a config file at `~/.localdsmc.yaml` or `~/.config/localdsmc/config.yaml`:

```yaml
# Server URL (default: ws://127.0.0.1:8080)
server_url: ws://127.0.0.1:8080

# Output directory for transcripts
output_dir: ~/transcripts

# Filename template with date placeholders
# Supported: %Y, %m, %d, %H, %M, %S
filename_template: transcript_%Y%m%d_%H%M%S.txt

# Audio gain multiplier (1.0 = normal)
gain: 1.0

# Audio device index (-1 = default)
device_index: -1
```

See `config.example.yaml` for all options.

## Usage

```bash
# Start transcription (requires moshi-server running)
./localdsmc

# List audio devices
./localdsmc -list-devices

# Use specific device and gain
./localdsmc -device 1 -gain 2.0

# Override output location
./localdsmc -output-dir /tmp -template recording_%Y%m%d.txt

# Direct output file
./localdsmc -o /tmp/my-transcript.txt

# Debug mode
./localdsmc -debug

# Show version
./localdsmc -version
```

## CLI Flags

| Flag | Short | Description |
|------|-------|-------------|
| `-config` | `-c` | Path to config file |
| `-server` | `-s` | WebSocket server URL |
| `-api-key` | | API key for authentication |
| `-output-dir` | `-d` | Output directory |
| `-template` | `-t` | Filename template |
| `-output` | `-o` | Direct output file path |
| `-gain` | `-g` | Audio gain multiplier |
| `-device` | | Audio device index |
| `-vad-pause` | | Pause on VAD end-of-turn |
| `-pause-threshold` | | Silence threshold (seconds) |
| `-debug` | | Enable debug output |
| `-list-devices` | `-l` | List audio devices |
| `-version` | `-v` | Show version |

CLI flags override config file values.

## Output

Transcripts are written to both:
- **stdout** - real-time display
- **file** - persistent storage (append mode)

The post-processor adds sensible line breaks:
- Paragraph breaks on VAD end-of-turn
- Line breaks at sentence boundaries
- Line breaks after silence pauses
- Soft wrap at ~80 characters

## Development

Run tests:
```bash
go test ./...
```

Run tests with coverage:
```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## License

MIT
