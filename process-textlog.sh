#!/usr/bin/env bash

# Continuously reads new lines from transcription.log, buffering them
# until we see an in-band flush marker (###FLUSH###). Then it sends
# the buffered text to the OpenAI Chat API along with a system prompt.
# If the request fails, prints an error message. If it succeeds, prints
# the assistant's content.
#
# The script is re-entrant: it resumes from the last offset in the log,
# so it doesn’t resend previously processed lines or flush commands.

set -euo pipefail

if [ -z "$OPENAI_API_KEY" ]; then
  echo "Missing OPENAI_API_KEY!"
  exit 2
fi

SYSTEM_PROMPT="You are a helpful ai agent responding to transcribed audio \
from work meetings. Analyze the text shown, and assume that it is part of \
a conversation in progresAnalyze the text from the conversation in progress. \
Extract the salient points and provide a terse, concise summary of helpful \
replies, always from a cybersecurity and engineering perspective. Limit \
output to one sentence per unique thought. If your output contains more than \
one thought, emit them as a bullet-point list."

NOW=$(date +%Y%m%d_%H%M)
LOG_FILE=${TRANSCRIPTION_FILE:-transcription-$NOW.log}
OFFSET_FILE="${OFFSET_FILE:-/tmp/transcription_offset-$NOW}"  # Stores the last read byte offset
BATCH=""  # Accumulated lines so far

main() {
  heartbeat & # Launch the heartbeat in the background 
  init_offset
  while true; do
   new_offset=$(wc -c < "$LOG_FILE" 2>/dev/null || echo 0)
 
   if [[ "$new_offset" -gt "$offset" ]]; then
     bytes_to_read=$(( new_offset - offset ))
 
     # skip old content
     chunk=$(dd if="$LOG_FILE" bs=1 skip="$offset" count="$bytes_to_read" 2>/dev/null)
 
     offset=$new_offset
     echo "$offset" > "$OFFSET_FILE"
 
     # Process each line in the chunk
     while IFS= read -r line; do
       if [[ "$line" == "###FLUSH###" ]]; then
         # We hit the in-band flush marker → send the accumulated lines to the API
         prompt_ai
         BATCH=""  # Clear the accumulated text after flushing
       else
         # Not a flush marker, accumulate line in BATCH
         BATCH+="$line\n"
       fi
     done <<< "$chunk"
   fi
 
   # Sleep briefly to avoid a tight loop
   sleep 1
 done
}

heartbeat() {
  while true; do
    date >> "$LOG_FILE"
    sleep 60
  done
}

prompt_ai() {
  # Save output to a temporary file so we can parse both output + HTTP status
  tmpfile=$(mktemp)
  http_code=$(curl -s -o "$tmpfile" -w "%{http_code}" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $OPENAI_API_KEY" \
    -d "{
      \"model\": \"gpt-3.5-turbo\",
      \"messages\": [
        {\"role\": \"system\",    \"content\": \"$SYSTEM_PROMPT\"},
        {\"role\": \"user\",      \"content\": \"$BATCH\"}
      ]
    }" \
    https://api.openai.com/v1/chat/completions)
 
  if [[ "$http_code" -ne 200 ]]; then
    # Parse out an error message if present
    error_msg=$(jq -r '.error.message // "Unknown error"' "$tmpfile" 2>/dev/null)
    echo "Error: $error_msg" >&2
  else
    # Extract the assistant's content, preserving newlines
    content=$(jq -r '.choices[0].message.content // ""' "$tmpfile" 2>/dev/null)
    echo "$content"
  fi
 
  rm -f "$tmpfile"
}

init_offset() {
  # Initialize offset from file, or 0 if it doesn't exist yet
  if [[ -f "$OFFSET_FILE" ]]; then
    offset=$(cat "$OFFSET_FILE")
  else
    offset=0
  fi
  echo "Offset: $offset"
}

set -x

main $*