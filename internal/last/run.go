// Package last implements the localscribe last subcommand.
package last

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LogLine is a parsed transcript or metadata line with timing data.
type LogLine struct {
	Timestamp      time.Time
	Raw            string
	IsMetadata     bool
	IsMeetingStart bool
	IsMeetingEnd   bool
}

// MeetingInterval captures start/end indices and times for a meeting.
type MeetingInterval struct {
	StartIndex int
	EndIndex   int
	StartTime  time.Time
	EndTime    time.Time
}

// Unit* constants define supported time window units.
const (
	UnitMinutes = iota
	UnitHours
	UnitDays
	UnitWeeks
	UnitMonths
	UnitMeetings
)

const (
	lineLayout   = "2006/01/02 15:04:05 MST"
	usageExample = `
Usage: localscribe last [--dir path] [--keepmeta] [--asof "YYYY/MM/DD HH:MM:SS MST"] <N> <unit>

Examples:
  localscribe last 20 min
  localscribe last 3 hours
  localscribe last 2 days
  localscribe last 1 week
  localscribe last 2 meetings
  localscribe last --asof="2024/12/31 23:45:00 EST" 2 hours

Note: Place flags *before* N/unit in Go’s default flag parsing, e.g.:
  localscribe last --asof="2024/12/31 23:45:00 EST" 2 meetings
`
)

var (
	// Old format: "2024/01/15 14:30:00 EST - transcript text"
	tsRegex = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2}\s+[A-Z]{3})\s+(.*)$`)

	// New format heartbeat: "%% time: 2024/01/15 14:30:00 EST"
	heartbeatRegex = regexp.MustCompile(`^%%\s*time:\s*(\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2}\s+[A-Z]{3})`)

	// New format meeting markers: "%% meeting started: 2024/01/15 14:30:00 EST zoom"
	newMeetingStartRegex = regexp.MustCompile(`^%%\s*meeting started:`)
	newMeetingEndRegex   = regexp.MustCompile(`^%%\s*meeting ended:`)
)

var errUsage = errors.New("invalid usage")

// Options holds flag values for the last subcommand.
type Options struct {
	Dir      string
	KeepMeta bool
	TrimDate bool
	AsOf     string
	AsOfUsed bool
}

func computeTimeCutoff(n int, unit int, baseTime time.Time) time.Time {
	switch unit {
	case UnitMinutes:
		return baseTime.Add(-time.Duration(n) * time.Minute)
	case UnitHours:
		return baseTime.Add(-time.Duration(n) * time.Hour)
	case UnitDays:
		return baseTime.AddDate(0, 0, -n)
	case UnitWeeks:
		return baseTime.AddDate(0, 0, -7*n)
	case UnitMonths:
		return baseTime.AddDate(0, -n, 0)
	default:
		return baseTime // fallback
	}
}

// Usage prints help for the last subcommand.
func Usage(w io.Writer) {
	fmt.Fprint(w, usageExample)
}

// Run executes the last subcommand with the provided arguments.
func Run(args []string, stdout, stderr io.Writer) error {
	opts, remaining, err := parseFlags(args)
	if err != nil {
		return err
	}

	if len(remaining) < 2 {
		Usage(stderr)
		return errUsage
	}
	nStr := remaining[0]
	unitStr := remaining[1]

	n, err := strconv.Atoi(nStr)
	if err != nil {
		return fmt.Errorf("invalid N '%s': %w", nStr, err)
	}
	unit, err := parseUnit(unitStr)
	if err != nil {
		return fmt.Errorf("unrecognized unit '%s': %w", unitStr, err)
	}

	dir := determineDirectory(opts.Dir)

	allLines, err := readAndParseAll(dir)
	if err != nil {
		return fmt.Errorf("failed reading logs: %w", err)
	}
	if len(allLines) == 0 {
		return nil
	}

	// Sort ascending
	sort.Slice(allLines, func(i, j int) bool {
		return allLines[i].Timestamp.Before(allLines[j].Timestamp)
	})

	currentTime, err := asOfTime(opts.AsOf)
	if err != nil {
		return err
	}

	switch unit {
	case UnitMinutes, UnitHours, UnitDays, UnitWeeks, UnitMonths:
		var filtered []LogLine
		if opts.AsOfUsed {
			// If --asof is used, filter within the N-unit window ending at asOfTime
			lowerBound := computeTimeCutoff(n, unit, currentTime)
			filtered = filterByTimeRange(allLines, lowerBound, currentTime)
		} else {
			// If --asof is NOT used, filter from N units ago until the end of logs.
			// This means we are showing logs from the cutoff up to the last available log line,
			// which matches the "last N units from now" expectation.
			cutoff := computeTimeCutoff(n, unit, currentTime) // currentTime is time.Now() here
			filtered = filterByTimeAfter(allLines, cutoff)
		}

		if !opts.KeepMeta {
			filtered = removeAllMetadata(filtered)
		}
		if opts.TrimDate {
			filtered = removeDatePrefix(filtered)
		}
		for _, ln := range filtered {
			fmt.Fprintln(stdout, ln.Raw)
		}

	case UnitMeetings:
		// Meeting-based => lines <= asOfTime => last N intervals
		linesBeforeAsOf := filterByTimeBefore(allLines, currentTime)

		intervals := findMeetingIntervals(linesBeforeAsOf)
		if len(intervals) == 0 {
			fmt.Fprintln(stderr, "Warning: No 'meeting started' lines found before asof. Returning no data.")
			return nil
		}

		var selected []MeetingInterval
		if len(intervals) >= n {
			selected = intervals[len(intervals)-n:]
		} else {
			selected = intervals
			fmt.Fprintf(stderr, "Warning: Only %d intervals found, asked for %d.\n", len(intervals), n)
		}

		gatherAndPrintMeetings(stdout, linesBeforeAsOf, selected, opts.KeepMeta, opts.TrimDate)
	}

	return nil
}

func parseFlags(args []string) (*Options, []string, error) {
	opts := &Options{}
	fs := flag.NewFlagSet("localscribe last", flag.ContinueOnError)
	fs.StringVar(&opts.Dir, "dir", "", "Transcription directory (overrides env TRANSCRIPTION_DIR).")
	fs.BoolVar(&opts.KeepMeta, "keepmeta", false, "Keep all metadata lines (instead of hiding them).")
	fs.BoolVar(&opts.TrimDate, "trimdate", false, "Remove datestamps from start of lines.")
	fs.StringVar(&opts.AsOf, "asof", "", `Use this "YYYY/MM/DD HH:MM:SS MST" instead of time.Now() for filtering.`)

	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}

	fs.Visit(func(f *flag.Flag) {
		if f.Name == "asof" {
			opts.AsOfUsed = true
		}
	})

	return opts, fs.Args(), nil
}

func removeDatePrefix(lines []LogLine) []LogLine {
	// Regex explanation:
	// ^                     start of the string
	// [0-9]{4}              4 digits of year
	// /[0-9]{2}/[0-9]{2}    slash-separated month/day
	// \s+                   one or more spaces
	// [0-9]{2}:[0-9]{2}:[0-9]{2} HH:MM:SS
	// \s+                   one or more spaces
	// [A-Z]{1,5}            1 to 5 uppercase letters for timezone (e.g. PST, MST, UTC)
	// \s* zero or more trailing spaces
	re := regexp.MustCompile(`^[0-9]{4}/[0-9]{2}/[0-9]{2}\s+[0-9]{2}:[0-9]{2}:[0-9]{2}\s+[A-Z]{1,5}\s*-\s*`)

	result := make([]LogLine, len(lines))
	for i, line := range lines {
		stripped := re.ReplaceAllString(line.Raw, "")
		// Update and store
		line.Raw = stripped
		result[i] = line
	}
	return result
}

func parseUnit(unitStr string) (int, error) {
	u := strings.ToLower(unitStr)
	switch u {
	case "m", "min", "mins", "minute", "minutes":
		return UnitMinutes, nil
	case "h", "hour", "hours":
		return UnitHours, nil
	case "d", "day", "days":
		return UnitDays, nil
	case "w", "week", "weeks":
		return UnitWeeks, nil
	case "mo", "month", "months":
		return UnitMonths, nil
	case "meet", "meeting", "meetings":
		return UnitMeetings, nil
	}
	return -1, errors.New("invalid unit")
}

func asOfTime(asOfStr string) (time.Time, error) {
	if asOfStr == "" {
		return time.Now(), nil
	}
	parsed, err := time.Parse(lineLayout, asOfStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse --asof '%s' with layout '%s': %w", asOfStr, lineLayout, err)
	}
	return parsed, nil
}

func determineDirectory(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if envVal := os.Getenv("TRANSCRIPTION_DIR"); envVal != "" {
		return envVal
	}
	home, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(home, ".local", "scribe")
	}
	return "./"
}

func gatherLogFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Support both .log (aai format) and .txt (localscribe format)
		if strings.HasSuffix(e.Name(), ".log") || strings.HasSuffix(e.Name(), ".txt") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func readAndParseAll(dir string) ([]LogLine, error) {
	files, err := gatherLogFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	var all []LogLine
	for _, fpath := range files {
		lines, err := readAndParseFile(fpath)
		if err != nil {
			// Log warning and continue with other files
			log.Printf("Warning: skipping %s: %v", filepath.Base(fpath), err)
			continue
		}
		all = append(all, lines...)
	}
	return all, nil
}

func readAndParseFile(fpath string) ([]LogLine, error) {
	// #nosec G304 -- log file paths come from user-controlled directories.
	f, err := os.Open(fpath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Track the current heartbeat timestamp for files using the new format
	var currentHeartbeat time.Time

	// Try to extract start time from filename for fallback
	// Format: transcript_YYYYMMDD_HHMMSS.txt
	baseName := filepath.Base(fpath)
	if m := regexp.MustCompile(`(\d{8})_(\d{6})`).FindStringSubmatch(baseName); len(m) == 3 {
		if t, err := time.Parse("20060102_150405", m[1]+"_"+m[2]); err == nil {
			currentHeartbeat = t
		}
	}

	// Use larger buffer to handle long lines (1MB max)
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)   // 64KB initial buffer
	scanner.Buffer(buf, 1024*1024) // 1MB max token size

	var lines []LogLine
	for scanner.Scan() {
		raw := scanner.Text()

		// Skip empty lines
		if strings.TrimSpace(raw) == "" {
			continue
		}

		ln, ok := parseLogLineWithHeartbeat(raw, &currentHeartbeat)
		if ok {
			lines = append(lines, ln)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}

func parseLogLine(raw string) (LogLine, bool) {
	m := tsRegex.FindStringSubmatch(raw)
	if len(m) != 3 {
		return LogLine{}, false
	}
	parsedTime, err := time.Parse(lineLayout, m[1])
	if err != nil {
		return LogLine{}, false
	}
	rest := strings.TrimSpace(m[2])
	ln := LogLine{
		Timestamp: parsedTime,
		Raw:       raw,
	}
	if strings.HasPrefix(rest, "%%%") || strings.HasPrefix(rest, "###") {
		ln.IsMetadata = true
	}
	if strings.Contains(rest, "%%% meeting started") {
		ln.IsMeetingStart = true
	}
	if strings.Contains(rest, "%%% meeting ended") {
		ln.IsMeetingEnd = true
	}
	return ln, true
}

// parseLogLineWithHeartbeat handles both old and new log formats.
// For new format, it tracks heartbeat timestamps and assigns them to plain text lines.
func parseLogLineWithHeartbeat(raw string, currentHeartbeat *time.Time) (LogLine, bool) {
	// First, try the old format (timestamp prefix on every line)
	if m := tsRegex.FindStringSubmatch(raw); len(m) == 3 {
		parsedTime, err := time.Parse(lineLayout, m[1])
		if err != nil {
			return LogLine{}, false
		}
		rest := strings.TrimSpace(m[2])
		ln := LogLine{
			Timestamp: parsedTime,
			Raw:       raw,
		}
		if strings.HasPrefix(rest, "%%%") || strings.HasPrefix(rest, "###") {
			ln.IsMetadata = true
		}
		if strings.Contains(rest, "%%% meeting started") {
			ln.IsMeetingStart = true
		}
		if strings.Contains(rest, "%%% meeting ended") {
			ln.IsMeetingEnd = true
		}
		// Update heartbeat for consistency
		*currentHeartbeat = parsedTime
		return ln, true
	}

	// New format: check for heartbeat timestamp line
	if m := heartbeatRegex.FindStringSubmatch(raw); len(m) == 2 {
		parsedTime, err := time.Parse(lineLayout, m[1])
		if err != nil {
			return LogLine{}, false
		}
		*currentHeartbeat = parsedTime
		ln := LogLine{
			Timestamp:  parsedTime,
			Raw:        raw,
			IsMetadata: true, // Heartbeat lines are metadata
		}
		return ln, true
	}

	// New format: check for meeting markers
	if newMeetingStartRegex.MatchString(raw) {
		ln := LogLine{
			Timestamp:      *currentHeartbeat,
			Raw:            raw,
			IsMetadata:     true,
			IsMeetingStart: true,
		}
		return ln, true
	}
	if newMeetingEndRegex.MatchString(raw) {
		ln := LogLine{
			Timestamp:    *currentHeartbeat,
			Raw:          raw,
			IsMetadata:   true,
			IsMeetingEnd: true,
		}
		return ln, true
	}

	// New format: check for other metadata lines (starting with %%)
	if strings.HasPrefix(raw, "%%") {
		ln := LogLine{
			Timestamp:  *currentHeartbeat,
			Raw:        raw,
			IsMetadata: true,
		}
		return ln, true
	}

	// New format: plain transcript text (use current heartbeat timestamp)
	if !currentHeartbeat.IsZero() {
		ln := LogLine{
			Timestamp: *currentHeartbeat,
			Raw:       raw,
		}
		return ln, true
	}

	// No timestamp available - skip this line
	return LogLine{}, false
}

// filterByTimeRange returns all lines whose timestamp is between lower and upper bounds (inclusive).
func filterByTimeRange(lines []LogLine, lower, upper time.Time) []LogLine {
	var out []LogLine
	for _, ln := range lines {
		if (ln.Timestamp.After(lower) || ln.Timestamp.Equal(lower)) &&
			(ln.Timestamp.Before(upper) || ln.Timestamp.Equal(upper)) {
			out = append(out, ln)
		}
	}
	return out
}

// filterByTimeAfter returns all lines whose timestamp >= cutoff
func filterByTimeAfter(lines []LogLine, cutoff time.Time) []LogLine {
	var out []LogLine
	for _, ln := range lines {
		if ln.Timestamp.Equal(cutoff) || ln.Timestamp.After(cutoff) {
			out = append(out, ln)
		}
	}
	return out
}

// filterByTimeBefore returns all lines whose timestamp <= cutoff
func filterByTimeBefore(lines []LogLine, cutoff time.Time) []LogLine {
	var out []LogLine
	for _, ln := range lines {
		if ln.Timestamp.Before(cutoff) || ln.Timestamp.Equal(cutoff) {
			out = append(out, ln)
		}
	}
	return out
}

func removeAllMetadata(lines []LogLine) []LogLine {
	var out []LogLine
	for _, ln := range lines {
		if ln.IsMetadata {
			continue
		}
		out = append(out, ln)
	}
	return out
}

func findMeetingIntervals(all []LogLine) []MeetingInterval {
	var intervals []MeetingInterval
	startIdx := -1

	for i, ln := range all {
		if ln.IsMeetingStart {
			// if there's an open meeting, close it at i-1
			if startIdx != -1 {
				intervals = append(intervals, MeetingInterval{
					StartIndex: startIdx,
					EndIndex:   i - 1,
					StartTime:  all[startIdx].Timestamp,
					EndTime:    all[i-1].Timestamp,
				})
			}
			startIdx = i
		} else if ln.IsMeetingEnd && startIdx != -1 {
			intervals = append(intervals, MeetingInterval{
				StartIndex: startIdx,
				EndIndex:   i,
				StartTime:  all[startIdx].Timestamp,
				EndTime:    ln.Timestamp,
			})
			startIdx = -1
		}
	}

	// unclosed meeting
	if startIdx != -1 {
		lastIdx := len(all) - 1
		intervals = append(intervals, MeetingInterval{
			StartIndex: startIdx,
			EndIndex:   lastIdx,
			StartTime:  all[startIdx].Timestamp,
			EndTime:    all[lastIdx].Timestamp,
		})
	}
	return intervals
}

func gatherAndPrintMeetings(w io.Writer, all []LogLine, intervals []MeetingInterval, keepAllMetadata bool, trimDate bool) {
	if trimDate {
		all = removeDatePrefix(all)
	}
	for ivIdx, iv := range intervals {
		for i := iv.StartIndex; i <= iv.EndIndex; i++ {
			ln := all[i]
			if !keepAllMetadata {
				// skip metadata unless it's meeting start/end
				if ln.IsMetadata && !(ln.IsMeetingStart || ln.IsMeetingEnd) {
					continue
				}
			}

			fmt.Fprintln(w, ln.Raw)
		}
		if ivIdx < len(intervals)-1 {
			fmt.Fprintln(w, "======")
		}
	}
}
