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

type LogLine struct {
	Timestamp      time.Time
	Raw            string
	IsMetadata     bool
	IsMeetingStart bool
	IsMeetingEnd   bool
}

type MeetingInterval struct {
	StartIndex int
	EndIndex   int
	StartTime  time.Time
	EndTime    time.Time
}

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
Usage: lastscribed <N> <unit> [--dir path] [--keepmeta] [--asof "YYYY/MM/DD HH:MM:SS MST"]

Examples:
  lastscribed 20 min
  lastscribed 3 hours
  lastscribed 2 days
  lastscribed 1 week
  lastscribed 2 meetings
  lastscribed 2 hours --asof="2024/12/31 23:45:00 EST"

Note: Place flags *before* N/unit in Go’s default flag parsing, e.g.:
  lastscribed --asof="2024/12/31 23:45:00 EST" 2 meetings
`
)

var (
	tsRegex = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2}\s+[A-Z]{3})\s+(.*)$`)

	keepMeta         bool
	trimDate         bool
	userSpecifiedDir string
	asOfStr          string
)

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

func main() {
	flag.StringVar(&userSpecifiedDir, "dir", "",
		"Transcription directory (overrides env TRANSCRIPTION_DIR).")
	flag.BoolVar(&keepMeta, "keepmeta", false,
		"Keep all metadata lines (instead of hiding them).")
	flag.BoolVar(&trimDate, "trimdate", false,
		"Remove datestamps from start of lines.")
	flag.StringVar(&asOfStr, "asof", "",
		`Use this "YYYY/MM/DD HH:MM:SS MST" instead of time.Now() for filtering.`)
	flag.Parse()

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

	dir := determineDirectory(userSpecifiedDir)

	allLines, err := readAndParseAll(dir)
	if err != nil {
		log.Fatalf("Failed reading logs: %v\n", err)
	}
	if len(allLines) == 0 {
		return
	}

	// Sort ascending
	sort.Slice(allLines, func(i, j int) bool {
		return allLines[i].Timestamp.Before(allLines[j].Timestamp)
	})

	switch unit {
	case UnitMinutes, UnitHours, UnitDays, UnitWeeks, UnitMonths:
		// Time-based query => lines >= (asOfTime - N)
		cutoff := computeTimeCutoff(n, unit, asOfTime())
		filtered := filterByTimeAfter(allLines, cutoff)
		if !keepMeta {
			filtered = removeAllMetadata(filtered)
		}
		if trimDate {
			filtered = removeDatePrefix(filtered)
		}
		for _, ln := range filtered {
			fmt.Println(ln.Raw)
		}

	case UnitMeetings:
		// Meeting-based => lines <= asOfTime => last N intervals
		cutoff := asOfTime()
		linesBeforeAsOf := filterByTimeBefore(allLines, cutoff)

		intervals := findMeetingIntervals(linesBeforeAsOf)
		if len(intervals) == 0 {
			log.Println("Warning: No 'meeting started' lines found before asof. Returning no data.")
			return
		}

		var selected []MeetingInterval
		if len(intervals) >= n {
			selected = intervals[len(intervals)-n:]
		} else {
			selected = intervals
			log.Printf("Warning: Only %d intervals found, asked for %d.\n", len(intervals), n)
		}

		gatherAndPrintMeetings(linesBeforeAsOf, selected, keepMeta)
	}
}

func removeDatePrefix(lines []LogLine) []LogLine {
	// Regex explanation:
	// ^                      start of the string
	// [0-9]{4}               4 digits of year
	// /[0-9]{2}/[0-9]{2}     slash-separated month/day
	// \s+                    one or more spaces
	// [0-9]{2}:[0-9]{2}:[0-9]{2}  HH:MM:SS
	// \s+                    one or more spaces
	// [A-Z]{1,5}             1 to 5 uppercase letters for timezone (e.g. PST, MST, UTC)
	// \s*                    zero or more trailing spaces
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

func usageExit(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
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

func asOfTime() time.Time {
	if asOfStr == "" {
		return time.Now()
	}
	parsed, err := time.Parse(lineLayout, asOfStr)
	if err != nil {
		log.Fatalf("Failed to parse --asof '%s' with layout '%s': %v\n", asOfStr, lineLayout, err)
	}
	return parsed
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
		if strings.HasSuffix(e.Name(), ".log") {
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

func gatherAndPrintMeetings(all []LogLine, intervals []MeetingInterval, keepAllMetadata bool) {
	for ivIdx, iv := range intervals {
		for i := iv.StartIndex; i <= iv.EndIndex; i++ {
			ln := all[i]
			if !keepAllMetadata {
				// skip metadata unless it's meeting start/end
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
