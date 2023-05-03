# LocalScribe: Transcribe and Summarize MP3s in Local Directory

This is a python script that watches a directory for MP3 files. When a new file is encountered, it will convert the speech to text via OpenAI's Whisper API, summarize the text via OpenAI's GPT3 API, and chunk everything to fit within the limits of Whisper and GPT3 APIs. The script will also display the costs for both Whisper and GPT3 API usage.

## Use Cases

Transcribe downloaded podcasts, meeting notes, memo dictation, etc. In my case, I happen to use an app called [RecUp](https://apps.apple.com/us/app/recup-record-to-the-cloud/id416288287) that records to cloud storage that is also sync'ed locally to disk. I point LocalScribe at this sync'ed directory location for automatic transcription and summarization of RecUp MP3 recordings.

## Installation

To use this script, you will need to install the necessary requirements. You can do this by running the following command in your terminal:

`pip install -r requirements.txt`

## Usage

Copy `.env.example` to `.env`, and fill out the variables there.

Then, you can run the script by running the following command in your terminal:

`python localscribe.py`

## License

This project is licensed under the MIT License - see the LICENSE.md file for details.

## Credits

This is a fork and modification of [kyon-eth/podcast-summarizer](https://github.com/kyon-eth/podcast-summarizer).