# localscribe SPEC

This document defines the future-state design for a single top-level `localscribe` binary that combines
recording and log-query functionality. It describes intended CLI, module layout, and key behaviors.
It does not document the current, pre-merge state.

## Goals

- Ship one binary (`localscribe`) with a clear subcommand interface.
- Preserve the existing behavior of the recorder and log slicer while simplifying distribution.
- Keep responsibilities separated in code (recording vs querying) while sharing common packages.
- Avoid backward-compatibility shims; migrate fully to the new binary.

## Non-Goals

- Introducing new features or changing recording/transcript semantics.
- Changing file formats or log line conventions.
- Adding networked services or external dependencies beyond the existing set.

## CLI Design

### Top-level

```
localscribe <command> [flags]

Commands:
  record     Capture audio, transcribe, and append to logs
  last       Query recent transcript lines by time window or meeting count
  help       Show help for a command
```

### `record` (recorder)

```
localscribe record [flags]
```

Flags (preserve current recorder behavior):

- `--config, -c` Path to config file
- `--server, -s` WebSocket server URL
- `--api-key` API key for authentication
- `--output-dir, -d` Output directory for transcripts
- `--template, -t` Filename template
- `--output, -o` Direct output file path (overrides template)
- `--gain, -g` Audio gain multiplier
- `--device` Audio input device index
- `--vad-pause` Pause on VAD end-of-turn detection
- `--pause-threshold` Silence threshold for line break (seconds)
- `--debug` Enable debug output
- `--list-devices, -l` List available audio devices
- `--version, -v` Show version

Behavior:
- Load config file, then apply CLI overrides with the same merge rules as before.
- Validate config before starting transcription.
- `--list-devices` exits after listing devices (no recording).
- Output to stdout and append to the configured file.

### `last` (log query)

```
localscribe last [flags] <N> <unit>
```

Flags:

- `--dir` Transcription directory override
- `--keepmeta` Keep metadata lines in output
- `--trimdate` Remove date prefixes from lines
- `--asof "YYYY/MM/DD HH:MM:SS MST"` Use the given time as the window end

Units:

- `min|mins|minute|minutes`
- `hour|hours`
- `day|days`
- `week|weeks`
- `month|months`
- `meeting|meetings`

Behavior:
- If `--asof` is provided, the window is `[asof - N units, asof]`.
- If `--asof` is not provided, the window is `[now - N units, end of logs]`.
- For `meeting(s)`, select the last N meeting intervals prior to `asof`/now.

## Module Layout

```
.
├── cmd/
│   └── localscribe/
│       └── main.go          # Subcommand parsing and dispatch
├── internal/
│   ├── record/
│   │   ├── run.go            # Entry point for `localscribe record`
│   │   └── flags.go          # Flag parsing and config overrides
│   ├── last/
│   │   ├── run.go            # Entry point for `localscribe last`
│   │   └── parse.go          # Log parsing + filtering logic
│   ├── audio/
│   ├── client/
│   ├── config/
│   ├── meetings/
│   ├── plugins/
│   ├── processor/
│   └── writer/
├── config/
│   └── config.example.yaml
└── go.mod
```

Notes:
- Recording packages are preserved as-is under `internal/` and imported by `internal/record`.
- `internal/last` houses the log-slicing logic moved out of the old standalone binary.

## Testing Strategy

- `cmd/localscribe/main_test.go` covers subcommand routing and help output.
- `internal/record/*_test.go` covers recorder behavior and flag/config merging.
- `internal/last/*_test.go` covers log parsing, time windows, and meeting selection.

## Migration Constraints

- All behavior should remain consistent with the prior standalone binaries.
- No compatibility shims or wrapper binaries; users move to `localscribe` directly.
