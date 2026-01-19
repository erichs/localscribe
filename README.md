# localscribe

**localscribe** is a lightweight CLI for capturing live audio, streaming it to a `moshi-server` ASR endpoint, and writing transcripts plus metadata to timestamped log files. It also includes a `last` command to query recent transcript windows and meeting slices.

## Major functionality

- Live microphone capture (24kHz mono) with adjustable gain and device selection.
- Streaming transcription via WebSocket to `moshi-server` (`/api/asr-streaming`).
- Structured metadata lines for timestamps, meeting boundaries, and plugin output.
- Optional meeting detection for Zoom / Google Meet and plugin hooks at lifecycle events.
- Log slicing with `localscribe last` by time window or last N meetings.

## Prerequisites

- Go 1.18+ (for building).
- PortAudio for microphone capture: `brew install portaudio`.
- A running `moshi-server` with the ASR streaming module enabled.

### moshi-server setup

`localscribe` expects a WebSocket ASR endpoint at `/api/asr-streaming` and sends the API key in the `kyutai-api-key` header. By default it connects to `ws://127.0.0.1:8080` and will automatically append `/api/asr-streaming` if needed. The rest of this guide assumes you are using moshi-server locally, and you will only ever have 1 simultaneous client connection.

This repo includes a sample `moshi-server` config in `config/config-stt-en_fr-single.toml`. Make sure your server config includes:

- `modules.asr.path = "/api/asr-streaming"`
- `authorized_ids` includes the optional API key (default `public_token`) if using a shared moshi-server

#### Install moshi-server (via uv + Python 3.9)

`moshi-server` requires Python 3.9. The easiest isolated setup uses `uv`:

```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
uv python install 3.9
```

Build the server with the uv-managed Python:

```bash
# macOS (Apple Silicon / Intel)
PYO3_PYTHON="$(uv python find 3.9)" cargo install --features metal moshi-server --force

# Linux (NVIDIA GPU)
PYO3_PYTHON="$(uv python find 3.9)" cargo install --features cuda moshi-server --force
```

#### Create a runtime wrapper

`moshi-server` needs the uv Python libraries at runtime. Create a wrapper that exports `DYLD_LIBRARY_PATH`:

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

Adjust `cpython-3.9.25-macos-aarch64-none` if uv installed a different version (`ls ~/.local/share/uv/python/`).

#### Run moshi-server

```bash
# Single-client config (recommended for personal use)
~/bin/moshi-server-run worker --config ./config-stt-en_fr-single.toml
```

On first run, the server downloads model files (~2GB). When ready you should see:

```
INFO listening on http://0.0.0.0:8080
INFO starting asr loop 1
```

The WebSocket endpoint is: `ws://127.0.0.1:8080/api/asr-streaming`.

#### Cleanup

```bash
uv python uninstall 3.9
cargo uninstall moshi-server
rm ~/bin/moshi-server-run
```

## Build

```bash
make build
```

### macOS: Microphone Indicator

By default, CLI tools don't trigger the orange microphone indicator in the macOS menu bar. If you want the indicator to appear when localscribe is recording, build it as a signed app bundle:

```bash
make app
./LocalScribe.app/Contents/MacOS/localscribe record
```

This creates a `LocalScribe.app` bundle with the necessary `NSMicrophoneUsageDescription` and ad-hoc code signature. The app bundle is gitignored.

## Configuration

Copy the example config and edit as needed:

```bash
cp config/config.example.yaml ~/.config/localscribe/config.yaml
```

Key settings:

- `server_url`: WebSocket base URL for `moshi-server` (default `ws://127.0.0.1:8080`)
- `api_key`: API key passed as `kyutai-api-key`
- `output_dir`: Where transcripts are written
- `filename_template`: Filename template (e.g. `transcript_%Y%m%d_%H%M%S.txt`)
- `metadata.*`: heartbeat timestamps, meeting detection, and plugin hooks

## Usage

### Record a transcript

```bash
./localscribe record
```

Common flags:

- `--list-devices` to list audio input devices.
- `--device <index>` to select a device.
- `--output-dir` / `--template` / `--output` to control log files.
- `--server` and `--api-key` to override server connection settings.

### Query recent transcript windows

```bash
./localscribe last 20 min
./localscribe last 2 hours
./localscribe last 2 meetings
```

Useful flags:

- `--dir` to override the transcript directory (defaults to `TRANSCRIPTION_DIR` or `~/.local/scribe`).
- `--keepmeta` to include metadata lines in output.
- `--trimdate` to remove timestamp prefixes.
- `--asof` to query relative to a specific time.

## Metadata lines

Metadata lines are prefixed with `%%` and are written alongside plain transcript lines. Examples:

```
%% time: 2024/12/25 13:00:00 UTC
%% meeting started: 2024/12/25 13:02:00 UTC zoom
%% meeting ended: 2024/12/25 13:42:00 UTC zoom (duration: 40m)
```

Plugins can emit metadata too; each plugin line is tagged with its name.

## Plugins

Plugins are small shell commands that run at lifecycle events and can emit metadata lines into the transcript. Each line of plugin stdout becomes a metadata line with a `%% <plugin-name>:` prefix.

### Interface and semantics

- Configuration lives under `metadata.plugins` in the YAML config.
- Each plugin has `name`, `command`, `trigger`, and optional `interval` and `timeout`.
- `trigger` values: `on_start`, `on_meeting_start`, `on_meeting_end`, `periodic`.
- For `periodic`, `interval` is in seconds; 0 or missing means the plugin is skipped.
- `timeout` defaults to 5s; it accepts Go duration strings (e.g. `5s`, `1m`) or integer seconds.
- Stdout lines are appended as metadata; empty lines are ignored.
- Stderr is logged to stderr (always on failure, or in debug mode).
- Environment variables are provided for context:
  - `LOCALSCRIBE_EVENT`, `LOCALSCRIBE_TIMESTAMP`, `LOCALSCRIBE_OUTPUT_FILE`
  - Meeting events add `LOCALSCRIBE_MEETING_TYPE`, `LOCALSCRIBE_MEETING_CODE`, `LOCALSCRIBE_MEETING_TITLE`, and `LOCALSCRIBE_MEETING_DURATION` (seconds)

### Security model

Plugins run locally using `sh -c` with your user permissions and no sandboxing. Treat plugin commands as trusted code: they can read/write files, access the network, and inherit your environment. Keep plugin scripts under your control and review any third-party snippets before enabling them.

### Examples

Simple on-start marker:

```yaml
metadata:
  plugins:
    - name: session
      trigger: on_start
      command: "echo started=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
```

Periodic git context (every 5 minutes):

```yaml
metadata:
  plugins:
    - name: git
      trigger: periodic
      interval: 300
      command: "git rev-parse --abbrev-ref HEAD && git rev-parse --short HEAD"
```

See `config/config.example.yaml` for more plugin ideas and patterns.
