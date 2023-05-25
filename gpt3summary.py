import os
from dotenv import load_dotenv
import openai
import tiktoken

class GPT3Summarizer:
    def __init__(self, api_key, model_engine = "gpt-3.5-turbo", max_tokens = 4096):
        load_dotenv()
        openai.api_key = api_key
        self.model_engine = model_engine
        self.max_tokens = max_tokens
        self.openai_price = float(os.getenv("OPENAI_PRICING_CHAT")) if model_engine == "gpt-3.5-turbo" else float(os.getenv("OPENAI_PRICING_TEXT"))
        
    def num_tokens_from_string(self, string: str, encoding_name: str) -> int:
        # Returns the number of tokens in a text string
        encoding = tiktoken.get_encoding(encoding_name)
        num_tokens = len(encoding.encode(string))
        return num_tokens
    
    def process_gpt3(self, prompt):
        # Process a prompt with GPT-3
        if self.model_engine == "davinci":
            response = openai.Completion.create(
                engine='text-davinci-003',
                prompt=prompt,
                n=1,
                stop=None,
                temperature=0.7,
            )
            
            choices = response.choices
            if choices:
                content = choices[0].text.strip()
                total_tokens = response.usage.total_tokens
                return content
            else:
                print(response)
                Exception("No choices returned from GPT-3 API using model 'text-davinci-003'")
                
        elif self.model_engine == "gpt-3.5-turbo":
            response = openai.ChatCompletion.create(
                model="gpt-3.5-turbo",
                messages=[
                        {"role": "system", "content": "You are an AI assistant that summarizes transcripts"},
                        {"role": "user", "content": prompt},
                    ]
            )
            choices = response.choices
            if choices:
                content = choices[0].message.content
                total_tokens = response.usage.total_tokens
            else:
                print(response)
                Exception("No choices returned from GPT-3 API using model 'gpt-3.5-turbo'")
            
            return content, total_tokens
        
    def process_chunks(self, chunks):
        # Process each chunk with GPT-3
        tokens_used = 0
        summaries = []
        for chunk in chunks:

            # Define the GPT-3 prompt for summarization
            prompt = f"Summarize partial transcript into sentences:\n\n{chunk}\n\nSummary:"
            
            # Call the GPT-3 API to generate the summary
            summary, token_count = self.process_gpt3(prompt)
            print(f'\t↪ tokens used: {token_count}')
            
            tokens_used += token_count
            summaries.append(summary)
            
        return summaries, tokens_used 
                
    def split_into_chunks(self, transcript):
        chunks = []
        sentences = transcript.replace("\n", "").split(".") # split the unwrapped transcript into sentences
        buffer = ""
        token_count = 0
        for sentence in sentences:
            
            token_count += self.num_tokens_from_string(sentence, "gpt2")
            buffer += sentence + "."
            
            if token_count > 2350:
                chunks.append(buffer)
                buffer = ""
                token_count = 0
                
        if buffer:
            chunks.append(buffer)

        print(f'↪ Chunks: {len(chunks)} ({len(sentences)} sentences)')
        
        return chunks

    def fully_summarize_transcript(self, transcript):
        # Split the transcript into chunks
        chunks = self.split_into_chunks(transcript)
        print(f'↪ Transcript chunks: {len(chunks)}')
            
        # Process each chunk with GPT-3
        summaries, tokens_used = self.process_chunks(chunks)
        
        return "\n\n".join(summaries), tokens_used

    def brief_summarize_transcript(self, full_summary, max_sentences):
        # condense the full summary into a few sentences
        prompt = f"Instructions:\nSummarize the following text into a list of {max_sentences} sentences\nContextualize the topics to the transcript\nDon't mention the transcript itself in the summary.\n\nText: {full_summary}\n\nSummary:"
        summary, token_count = self.process_gpt3(prompt)
        return summary, token_count
 

    def summarize(self, audio_path, transcript, max_sentences, processed_dir):

        file_id = os.path.basename(audio_path)
        
        print(f'🤖 Initializing GPT-3 summarizer...')
        print(f'↪ Using model: {self.model_engine}')
        print(f'↪ Transcript characters: {len(transcript)}')

        full_summary, tokens_used = self.fully_summarize_transcript(transcript)
        
        # save the full summary to a file
        summary_file = os.path.join(processed_dir, 'gpt3', f"{file_id}_{self.model_engine}_full.txt")
        with open(summary_file, "w") as f:
            f.write(full_summary)
            print(f'↪ Full summary saved to {summary_file}')
            
        brief_summary, token_count = self.brief_summarize_transcript(full_summary, max_sentences)
        tokens_used += token_count

        # save the brief summary to a file
        summary_file = os.path.join(processed_dir, 'gpt3', f"{file_id}_{self.model_engine}_summary.txt")
        with open(summary_file, "w") as f:
            f.write(brief_summary)
            print(f'↪ Brief {max_sentences} sentence summary saved to {summary_file}')
        
        api_cost = float(tokens_used / 1000) * float(self.openai_price)
        print(f'↪ 💵 GPT3 cost: ${api_cost:.2f} ({tokens_used} tokens)')
        
