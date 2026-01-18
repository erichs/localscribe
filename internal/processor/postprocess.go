// Package processor handles post-processing of transcribed text.
package processor

import (
	"strings"
	"time"
	"unicode"
)

// Options configures the post-processor behavior.
type Options struct {
	PauseThreshold time.Duration // Insert line break after this silence duration
	MaxLineLength  int           // Soft wrap at this character count
}

// DefaultOptions returns sensible default options.
func DefaultOptions() Options {
	return Options{
		PauseThreshold: 2 * time.Second,
		MaxLineLength:  80,
	}
}

// PostProcessor handles formatting of transcribed text with sensible line breaks.
type PostProcessor struct {
	opts         Options
	lastWordTime time.Time
	currentLine  strings.Builder
	lastWord     string
	hasContent   bool
}

// New creates a new PostProcessor with the given options.
func New(opts Options) *PostProcessor {
	return &PostProcessor{
		opts: opts,
	}
}

// ProcessWord processes a transcribed word and returns the formatted output.
// The output includes any necessary line breaks and spacing.
func (p *PostProcessor) ProcessWord(word string) string {
	// Trim whitespace from word
	word = strings.TrimSpace(word)
	if word == "" {
		return ""
	}

	var result strings.Builder
	now := time.Now()

	// Check for long silence (pause)
	if p.hasContent && !p.lastWordTime.IsZero() {
		elapsed := now.Sub(p.lastWordTime)
		if elapsed > p.opts.PauseThreshold {
			result.WriteString("\n")
			p.currentLine.Reset()
		}
	}

	// Check for sentence boundary (previous word ended sentence, this starts with capital)
	if p.hasContent && endsWithSentenceEnd(p.lastWord) && startsWithCapital(word) {
		result.WriteString("\n")
		p.currentLine.Reset()
	}

	// Check line length (soft wrap)
	if p.currentLine.Len() > 0 && p.currentLine.Len()+1+len(word) > p.opts.MaxLineLength {
		result.WriteString("\n")
		p.currentLine.Reset()
	}

	// Add space if not at start of line
	if p.currentLine.Len() > 0 {
		result.WriteString(" ")
		p.currentLine.WriteString(" ")
	}

	// Add the word
	result.WriteString(word)
	p.currentLine.WriteString(word)

	p.lastWord = word
	p.lastWordTime = now
	p.hasContent = true

	return result.String()
}

// ProcessEndOfTurn handles VAD end-of-turn detection.
// Returns a paragraph break if there's content, empty string otherwise.
func (p *PostProcessor) ProcessEndOfTurn() string {
	if !p.hasContent {
		return ""
	}

	p.currentLine.Reset()
	p.lastWord = ""
	return "\n\n"
}

// Reset clears the processor state.
func (p *PostProcessor) Reset() {
	p.currentLine.Reset()
	p.lastWord = ""
	p.lastWordTime = time.Time{}
	p.hasContent = false
}

// CurrentLineLength returns the current line length.
func (p *PostProcessor) CurrentLineLength() int {
	return p.currentLine.Len()
}

// Common abbreviations that end with a period but aren't sentence endings
var abbreviations = map[string]bool{
	"dr.":     true,
	"mr.":     true,
	"mrs.":    true,
	"ms.":     true,
	"prof.":   true,
	"sr.":     true,
	"jr.":     true,
	"etc.":    true,
	"vs.":     true,
	"e.g.":    true,
	"i.e.":    true,
	"no.":     true,
	"vol.":    true,
	"rev.":    true,
	"est.":    true,
	"approx.": true,
}

// endsWithSentenceEnd returns true if the word ends with sentence-ending punctuation.
func endsWithSentenceEnd(word string) bool {
	if len(word) == 0 {
		return false
	}

	// Check for abbreviations
	lower := strings.ToLower(word)
	if abbreviations[lower] {
		return false
	}

	lastChar := rune(word[len(word)-1])
	return lastChar == '.' || lastChar == '!' || lastChar == '?'
}

// startsWithCapital returns true if the word starts with a capital letter.
func startsWithCapital(word string) bool {
	word = strings.TrimSpace(word)
	if len(word) == 0 {
		return false
	}

	for _, r := range word {
		return unicode.IsUpper(r)
	}
	return false
}
