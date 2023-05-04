import os
import time
import shutil
from dotenv import load_dotenv
from whispertranscription import WhisperTranscriber
from gpt3summary import GPT3Summarizer

# Load API credentials from .env file
load_dotenv()
OPENAI_API_KEY =  os.environ["OPENAI_API_KEY"] 
WATCHED_DIR = os.environ["WATCHED_DIR"]
PROCESSED_DIR = os.environ["PROCESSED_DIR"]

# Create the processed directories if they don't exist
for dir in [ os.path.join(PROCESSED_DIR, 'whisper'), os.path.join(PROCESSED_DIR, 'gpt3') ]:
    if not os.path.exists(dir):
        os.makedirs(dir, exist_ok=True)

def main_loop():
    last_size = {} # Keep track of file sizes for debounce logic
    shouldDisplayPolling = True

    while True:
        if shouldDisplayPolling:
            print(f"Watching {WATCHED_DIR} for new files (ctrl-c to quit)...")
            shouldDisplayPolling = False # only display once while polling

        # Iterate over each file in the watched directory
        files = os.listdir(WATCHED_DIR)
        for file in files:
            # Ignore non-MP3 files
            if not file.endswith(".mp3"):
                continue

            print(f"Found {file}, waiting for it to finish uploading...")
            shouldDisplayPolling = True  # reset polling display
            file_path = os.path.join(WATCHED_DIR, file)

            # Debounce: wait for the file size to stop changing
            while True:
                size = os.path.getsize(file_path)
                if file not in last_size or size > last_size[file]:
                    last_size[file] = size
                    time.sleep(2)
                else:
                    break

            print(f"Processing {file}...")
            transcribe_and_summarize(file_path)

            # Move the processed file to the processed directory
            processed_file_path = os.path.join(PROCESSED_DIR, file)
            shutil.move(file_path, processed_file_path)
            print(f"Moved {file} to {processed_file_path}")

        # Wait for a bit before polling again
        time.sleep(5)

def transcribe_and_summarize(audio_path, max_sentences=10):
    
    print(f'↪ audio_path: {audio_path}')
    print(f'↪ max_sentences: {max_sentences}')
    
    file_id = os.path.basename(audio_path)
    dir_name = os.path.dirname(audio_path)
    
    transcriber = WhisperTranscriber(OPENAI_API_KEY)
    transcript = transcriber.transcribe(audio_path, PROCESSED_DIR)
    
   
    summarizer = GPT3Summarizer(OPENAI_API_KEY, model_engine="gpt-3.5-turbo")
    summarizer.summarize(audio_path, transcript, max_sentences)
    
    print(f'Completed summarization for ({audio_path})')

if __name__ == "__main__":
   try:
      main_loop()
   except KeyboardInterrupt:
      print("\nExiting...") 
      pass
