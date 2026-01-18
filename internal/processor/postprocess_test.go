package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewPostProcessor(t *testing.T) {
	p := New(Options{
		PauseThreshold: 2 * time.Second,
		MaxLineLength:  80,
	})
	assert.NotNil(t, p)
}

func TestProcessWord(t *testing.T) {
	p := New(DefaultOptions())

	result := p.ProcessWord("hello")
	assert.Equal(t, "hello", result)

	result = p.ProcessWord("world")
	assert.Equal(t, " world", result)
}

func TestProcessWordSentenceBoundary(t *testing.T) {
	p := New(DefaultOptions())

	p.ProcessWord("Hello")
	p.ProcessWord("world.")

	// Next word starts with capital after sentence end
	result := p.ProcessWord("This")
	assert.Equal(t, "\nThis", result)
}

func TestProcessWordNoSentenceBreakMidSentence(t *testing.T) {
	p := New(DefaultOptions())

	p.ProcessWord("Hello")
	p.ProcessWord("world.")

	// Next word is lowercase - not a new sentence
	result := p.ProcessWord("and")
	assert.Equal(t, " and", result)
}

func TestProcessEndOfTurn(t *testing.T) {
	p := New(DefaultOptions())

	p.ProcessWord("Hello")
	p.ProcessWord("world")

	result := p.ProcessEndOfTurn()
	assert.Equal(t, "\n\n", result)

	// Next word should start fresh line
	result = p.ProcessWord("New")
	assert.Equal(t, "New", result)
}

func TestProcessLongLine(t *testing.T) {
	p := New(Options{
		MaxLineLength:  20,
		PauseThreshold: time.Hour,
	})

	// Build up a line
	p.ProcessWord("This")
	p.ProcessWord("is")
	p.ProcessWord("a")
	p.ProcessWord("test")

	// This should trigger a line break (line is now ~17 chars)
	result := p.ProcessWord("sentence")
	assert.Contains(t, result, "\n")
}

func TestProcessSilencePause(t *testing.T) {
	p := New(Options{
		PauseThreshold: 50 * time.Millisecond,
		MaxLineLength:  80,
	})

	p.ProcessWord("Hello")

	// Wait for pause threshold
	time.Sleep(100 * time.Millisecond)

	result := p.ProcessWord("world")
	assert.Contains(t, result, "\n")
}

func TestProcessQuestionMark(t *testing.T) {
	p := New(DefaultOptions())

	p.ProcessWord("How")
	p.ProcessWord("are")
	p.ProcessWord("you?")

	result := p.ProcessWord("I")
	assert.Equal(t, "\nI", result)
}

func TestProcessExclamationMark(t *testing.T) {
	p := New(DefaultOptions())

	p.ProcessWord("Wow!")

	result := p.ProcessWord("That")
	assert.Equal(t, "\nThat", result)
}

func TestProcessNoBreakAfterAbbreviation(t *testing.T) {
	p := New(DefaultOptions())

	p.ProcessWord("Dr.")

	// Lowercase after abbreviation - not a sentence break
	result := p.ProcessWord("smith")
	assert.Equal(t, " smith", result)
}

func TestProcessMultipleSentences(t *testing.T) {
	p := New(DefaultOptions())

	var output string
	output += p.ProcessWord("First")
	output += p.ProcessWord("sentence.")
	output += p.ProcessWord("Second")
	output += p.ProcessWord("sentence.")
	output += p.ProcessWord("Third")
	output += p.ProcessWord("one.")

	assert.Contains(t, output, "First sentence.")
	assert.Contains(t, output, "\nSecond sentence.")
	assert.Contains(t, output, "\nThird one.")
}

func TestReset(t *testing.T) {
	p := New(DefaultOptions())

	p.ProcessWord("Hello")
	p.ProcessWord("world.")

	p.Reset()

	// After reset, should start fresh
	result := p.ProcessWord("New")
	assert.Equal(t, "New", result)
}

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	assert.Equal(t, 2*time.Second, opts.PauseThreshold)
	assert.Equal(t, 80, opts.MaxLineLength)
}

func TestCurrentLineLength(t *testing.T) {
	p := New(DefaultOptions())

	assert.Equal(t, 0, p.CurrentLineLength())

	p.ProcessWord("Hello")
	assert.Equal(t, 5, p.CurrentLineLength())

	p.ProcessWord("world")
	assert.Equal(t, 11, p.CurrentLineLength()) // "Hello world"
}

func TestProcessEmptyWord(t *testing.T) {
	p := New(DefaultOptions())

	result := p.ProcessWord("")
	assert.Equal(t, "", result)
}

func TestProcessWordWithWhitespace(t *testing.T) {
	p := New(DefaultOptions())

	// Words from STT might have leading/trailing whitespace
	result := p.ProcessWord("  hello  ")
	assert.Equal(t, "hello", result)

	result = p.ProcessWord("  world  ")
	assert.Equal(t, " world", result)
}

func TestProcessEndOfTurnWhenEmpty(t *testing.T) {
	p := New(DefaultOptions())

	// End of turn with nothing written should return empty
	result := p.ProcessEndOfTurn()
	assert.Equal(t, "", result)
}

func TestProcessPunctuationOnly(t *testing.T) {
	p := New(DefaultOptions())

	p.ProcessWord("Hello")

	// Sometimes punctuation comes as separate token
	result := p.ProcessWord(".")
	assert.Equal(t, " .", result) // Just append the punctuation
}

func TestEndsWithSentenceEnd(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Hello.", true},
		{"Hello!", true},
		{"Hello?", true},
		{"Hello", false},
		{"Hello,", false},
		{"Dr.", false}, // Common abbreviation
		{"Mr.", false},
		{"Mrs.", false},
		{"Ms.", false},
		{"etc.", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := endsWithSentenceEnd(tt.input)
			assert.Equal(t, tt.expected, result, "input: %q", tt.input)
		})
	}
}

func TestStartsWithCapital(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Hello", true},
		{"hello", false},
		{"HELLO", true},
		{"123", false},
		{"", false},
		{" Hello", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := startsWithCapital(tt.input)
			assert.Equal(t, tt.expected, result, "input: %q", tt.input)
		})
	}
}
