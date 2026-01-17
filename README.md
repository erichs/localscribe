# localscribe

**localscribe** is a lightweight system for capturing and processing meeting transcripts, and metadata

**localscribe** (the logger)  
- Continuously writes lines to a single log file (`transcription.log`).  
- These lines include:
- **Transcript lines** (plain text, no prefix).  
- **Metadata lines** prefixed by `%%%` (e.g., timestamps, meeting details).  

---

## Goals of the Project

- Provide a **simple, single-writer** logging mechanism for meeting transcriptions and metadata.  

---

## Dependencies

To use `localscribe` with a microphone, you will need to install `portaudio`: `brew install portaudio`.

## Metadata Syntax 

- **Prefix**: `%%%`  
- **Examples**:  
  - `%%% heartbeat 2024-12-25T13:00:00Z` (timestamp marker)  
  - `%%% meeting topic=Holiday Planning` (arbitrary metadata)

These lines help downstream tools approximate time offsets and store contextual info.

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
```
