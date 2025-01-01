// File: main.go
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
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

// LogLine holds a parsed line from the transcription logs.
type LogLine struct {
	Timestamp time.Time
	Raw       string

	IsMetadata     bool // line starts with "%%%" or "###" after the timestamp
	IsMeetingStart bool
	IsMeetingEnd   bool
}

// MeetingInterval represents a meeting from start to end (or next start).
type MeetingInterval struct {
	StartIndex int
	EndIndex   int
	StartTime  time.Time
	EndTime    time.Time
}

// Constants for different units we support
const (
	UnitMinutes = iota
	UnitHours
	UnitDays
	UnitWeeks
	UnitMonths
	UnitMeetings
)

// Layout to parse "YYYY/MM/DD HH:MM:SS MST"
const (
	lineLayout   = "2006/01/02 15:04:05 MST"
	usageExample = `
Usage: lastscribed <N> <unit> [--dir path] [--keepmeta] [--asof "YYYY/MM/DD HH:MM:SS MST"]

Examples:
  lastscribed 20 min
  lastscribed 3 hours
  lastscribed 2 days
  lastscribed 1 week
  lastscribed 2 meetings
  lastscribed 2 hours --asof "2024/12/31 23:45:00 EST"
`
)

// Regex to capture prefix: [YYYY/MM/DD HH:MM:SS MST] then space, then rest
var tsRegex = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2}\s+[A-Z]{3})\s+(.*)$`)

// Flags
var (
	keepMeta         bool
	userSpecifiedDir string
	asOfStr          string // e.g. "2024/12/31 23:45:00 EST"
	foo              string // e.g. "2024/12/31 23:45:00 EST"
)

// main is the entry point for the CLI tool.
func main() {
	// 1. Define flags
	flag.StringVar(&userSpecifiedDir, "dir", "", "Transcription directory (overrides env TRANSCRIPTION_DIR).")
	flag.BoolVar(&keepMeta, "keepmeta", false, "Keep all metadata lines (instead of hiding them).")
	flag.StringVar(&asOfStr, "asof", "", `Use this "YYYY/MM/DD HH:MM:SS MST" instead of time.Now() for cutoff calculations.`)
	flag.Parse()

	// 2. Parse positional args: N, unit
	if len(flag.Args()) < 2 {
		usageExit(usageExample)
	}
	nStr := flag.Arg(0)
	unitStr := flag.Arg(1)

	n, err := strconv.Atoi(nStr)
	if err != nil {
		log.Fatalf("Invalid N '%s': %v\n", nStr, err)
	}
	unit, err := parseUnit(unitStr)
	if err != nil {
		log.Fatalf("Unrecognized unit '%s': %v\n", unitStr, err)
	}
	line := fmt.Sprintf("Using asof: %s\n", asOfStr)
	fmt.Println(line)

	// 3. Determine directory
	dir := determineDirectory(userSpecifiedDir)

	// 4. Read & parse all lines
	allLines, err := readAndParseAll(dir)
	if err != nil {
		log.Fatalf("Failed reading logs: %v\n", err)
	}
	if len(allLines) == 0 {
		// no data
		return
	}

	// 5. Sort lines by timestamp ascending
	sort.Slice(allLines, func(i, j int) bool {
		return allLines[i].Timestamp.Before(allLines[j].Timestamp)
	})

	// 6. Filter lines
	switch unit {
	case UnitMinutes, UnitHours, UnitDays, UnitWeeks, UnitMonths:
		// Time-based filter
		cutoff := computeTimeCutoff(n, unit, asOfTime())
		filtered := filterByTime(allLines, cutoff)
		if !keepMeta {
			filtered = removeAllMetadata(filtered)
		}
		// Print
		for _, ln := range filtered {
			fmt.Println(ln.Raw)
		}

	case UnitMeetings:
		// Meeting-based filter
		intervals := findMeetingIntervals(allLines)
		if len(intervals) == 0 {
			log.Println("Warning: No 'meeting started' lines found. Returning no data.")
			return
		}
		var selected []MeetingInterval
		if len(intervals) >= n {
			selected = intervals[len(intervals)-n:]
		} else {
			selected = intervals
			log.Printf("Warning: Only %d intervals found, user asked for %d.\n", len(intervals), n)
		}
		gatherAndPrintMeetings(allLines, selected, keepMeta)
	}
}

// usageExit prints a usage message and exits.
func usageExit(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// parseUnit checks if second arg is "m|min|minutes|h|hour|hours|days|weeks|months|meetings"
func parseUnit(unitStr string) (int, error) {
	u := strings.ToLower(unitStr)
	switch u {
	case "m", "min", "minute", "minutes":
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

// asOfTime parses the --asof string if given, otherwise returns time.Now().
func asOfTime() time.Time {
	if asOfStr == "" {
		return time.Now()
	}
	// parse with lineLayout "2006/01/02 15:04:05 MST"
	parsed, err := time.Parse(lineLayout, asOfStr)
	if err != nil {
		log.Fatalf("Failed to parse --asof '%s' with layout '%s': %v\n", asOfStr, lineLayout, err)
	}
	return parsed
}

// determineDirectory picks from --dir, then TRANSCRIPTION_DIR, else ~/.local/scribe
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

// gatherLogFiles returns the .log files in lexical order from the dir
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
		if strings.HasSuffix(e.Name(), ".log") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

// readAndParseAll reads all lines from .log files, storing only lines that match our timestamp layout.
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
		f, err := os.Open(fpath)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			raw := scanner.Text()
			ln, ok := parseLogLine(raw)
			if ok {
				all = append(all, ln)
			}
		}
		f.Close()
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	return all, nil
}

// parseLogLine tries to parse "YYYY/MM/DD HH:MM:SS MST" from start of line.
// If parsing fails, returns (LogLine{}, false).
func parseLogLine(raw string) (LogLine, bool) {
	var ln LogLine

	m := tsRegex.FindStringSubmatch(raw)
	if len(m) != 3 {
		return ln, false
	}
	tStr := m[1] // e.g. "2024/12/31 23:45:00 EST"
	rest := m[2]
	parsedTime, err := time.Parse(lineLayout, tStr)
	if err != nil {
		return ln, false
	}
	ln.Timestamp = parsedTime
	ln.Raw = raw

	trimmed := strings.TrimSpace(rest)
	if strings.HasPrefix(trimmed, "%%%") || strings.HasPrefix(trimmed, "###") {
		ln.IsMetadata = true
	}
	if strings.Contains(trimmed, "%%% meeting started") {
		ln.IsMeetingStart = true
	}
	if strings.Contains(trimmed, "%%% meeting ended") {
		ln.IsMeetingEnd = true
	}
	return ln, true
}

// computeTimeCutoff returns asOf minus the appropriate duration
func computeTimeCutoff(n int, unit int, asOf time.Time) time.Time {
	switch unit {
	case UnitMinutes:
		return asOf.Add(-time.Duration(n) * time.Minute)
	case UnitHours:
		return asOf.Add(-time.Duration(n) * time.Hour)
	case UnitDays:
		return asOf.AddDate(0, 0, -n)
	case UnitWeeks:
		return asOf.AddDate(0, 0, -7*n)
	case UnitMonths:
		return asOf.AddDate(0, -n, 0)
	default:
		return asOf // fallback
	}
}

// filterByTime returns all lines whose timestamp >= cutoff
func filterByTime(lines []LogLine, cutoff time.Time) []LogLine {
	var out []LogLine
	for _, ln := range lines {
		if ln.Timestamp.After(cutoff) || ln.Timestamp.Equal(cutoff) {
			out = append(out, ln)
		}
	}
	return out
}

// removeAllMetadata removes lines that are flagged as metadata (start with %%% or ###).
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

// findMeetingIntervals scans lines for 'meeting started' or 'meeting ended' to build intervals.
func findMeetingIntervals(all []LogLine) []MeetingInterval {
	var intervals []MeetingInterval
	startIdx := -1

	for i, ln := range all {
		if ln.IsMeetingStart {
			// if there's a start already open, close it
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

	// if there's an unclosed start
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

// gatherAndPrintMeetings prints lines in intervals, separated by "======" lines.
func gatherAndPrintMeetings(all []LogLine, intervals []MeetingInterval, keepAllMetadata bool) {
	for ivIdx, iv := range intervals {
		for i := iv.StartIndex; i <= iv.EndIndex; i++ {
			ln := all[i]
			// If we do not have --keepmeta, we still want to keep meeting start/end lines,
			// but skip other metadata (like %%% ipinfo, etc.).
			if !keepAllMetadata {
				if ln.IsMetadata && !(ln.IsMeetingStart || ln.IsMeetingEnd) {
					continue
				}
			}
			fmt.Println(ln.Raw)
		}
		if ivIdx < len(intervals)-1 {
			fmt.Println("======")
		}
	}
}
