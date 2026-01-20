// Package main provides the localscribe CLI.
package main

import (
	"fmt"
	"io"
	"os"

	"localscribe/internal/last"
	"localscribe/internal/record"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		return runHelp(args[1:], stdout, stderr)
	case "record":
		return record.Run(args[1:], stdout, stderr)
	case "last":
		return last.Run(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runHelp(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	switch args[0] {
	case "record":
		record.Usage(stdout)
		return nil
	case "last":
		last.Usage(stdout)
		return nil
	default:
		fmt.Fprintf(stderr, "Unknown command: %s\n", args[0])
		printUsage(stderr)
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: localscribe <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  record     Capture audio, transcribe, and append to logs")
	fmt.Fprintln(w, "  last       Query recent transcript lines by time window or meeting count")
	fmt.Fprintln(w, "  help       Show help for a command")
}
