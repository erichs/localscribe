# localscribe

**localscribe** is a lightweight, two-part system for capturing and processing meeting transcripts, metadata, and real-time AI commands. It consists of:

1. **localscribe** (the logger)  
   - Continuously writes lines to a single log file (`transcription.log`).  
   - These lines include:
     - **Transcript lines** (plain text, no prefix).  
     - **Metadata lines** prefixed by `%%%` (e.g., timestamps, meeting details).  
     - **Command lines** prefixed by `###` (e.g., “last N | summarize”).

2. **herald** (the watcher)  
   - Reads (`tails`) the log file, parsing any command lines (`###`), and triggers AI or other external processing.  
   - Uses approximate time-based retrieval keyed by `%%% heartbeat <timestamp>` lines (once per minute).

---

## Goals of the Project

- Provide a **simple, single-writer** logging mechanism for meeting transcriptions and metadata.  
- Offer an **in-band command** approach: user issues instructions by writing special lines (`###`) directly into the same log file.  
- Enable a **watcher** process to spot those commands and **dispatch** them to external tools (like [fabric](https://github.com/danielmiessler/fabric)) for summarization or analysis.

---

## Dependencies

To use `localscribe` with a microphone, you will need to install `portaudio`: `brew install portaudio`.

## Syntax Conventions

### 1. Metadata Lines

- **Prefix**: `%%%`  
- **Examples**:  
  - `%%% heartbeat 2024-12-25T13:00:00Z` (timestamp marker)  
  - `%%% meeting topic=Holiday Planning` (arbitrary metadata)

These lines help the watcher approximate time offsets and store contextual info.

### 2. Command Lines

- **Prefix**: `###`  
- **Format**: `### last <N> | <action>`  
- **Example**:  
  - `### last 5 | summarize`  
  - `### last 10 | extract_main_idea`  
  - `### last 2 | extract_extraordinary_claims`

When `herald` sees a line like this, it retrieves roughly the last `<N>` minutes of transcript (ignoring all metadata or further commands) and pipes them to an external tool (e.g., `fabric --pattern <action>`).

### 3. Transcript Lines

- **No special prefix**  
- **Examples**:  
  - `I'm trying to replicate a 10-foot snow sculpture in my backyard.`  
  - `We need to review the design docs before the next sprint.`

These lines are simply appended to the log by `localscribe` as users speak or as other events happen.

---

## How to Run

### Prerequisites

- **Go** (1.18+ recommended)  
- Optional: **[fabric](https://github.com/danielmiessler/fabric)** for AI patterns

---

### 1. localscribe (Logger)

`localscribe` is responsible for writing transcript, metadata, and command lines to the log file. You can create or adapt your own script/application to append lines:

```bash
echo "%%% heartbeat $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> transcription.log
echo "This is a normal transcript line." >> transcription.log
echo "### last 3 | summarize" >> transcription.log
