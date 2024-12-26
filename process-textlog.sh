#!/usr/bin/env bash

set -euo pipefail

LOGFILE=${TRANSCRIPTION_FILE:-transcription.log}
if [ -z "$OPENAI_API_KEY" ]; then
  echo "Missing OPENAI_API_KEY!"
  exit 2
fi

SYSTEM_PROMPT="You are a helpful ai agent responding to transcribed audio from work meetings. Analyze the text shown, and assume that it is part of a conversation in progresAnalyze the text from the conversation in progress. Extract the salient points and provide a terse, concise summary of helpful replies, always from a cybersecurity and engineering perspective. Limit output to one sentence per unique thought. If your output contains more than one thought, emit them as a bullet-point list."
BATCH=""

tail -F $LOGFILE | while read -r line; do
  if [[ "$line" == "###FLUSH###" ]]; then
    # We received the in-band flush marker.
    # Send the accumulated batch (if any).

    if [[ -n "$BATCH" ]]; then
      echo "Flushing queue to API..."

      # Example: Send to OpenAI Chat Completions
      # This is just an illustration with curl:
      curl -s https://api.openai.com/v1/chat/completions \
           -H "Content-Type: application/json" \
           -H "Authorization: Bearer $OPENAI_API_KEY" \
           -d "{
                 \"model\": \"gpt-4o-mini\",
                 \"messages\": [
                   {\"role\": \"system\", \"content\": \"$SYSTEM_PROMPT\"},
                   {\"role\": \"user\", \"content\": \"$BATCH\"}
                 ]
               }"

      # Clear the batch after flushing
      BATCH=""
    fi
  else
    # Accumulate the new line into BATCH
    BATCH+="${line}\n"
  fi
done
