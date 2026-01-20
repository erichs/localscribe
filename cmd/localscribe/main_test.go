package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run([]string{}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: localscribe") {
		t.Fatalf("expected usage in stdout")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run([]string{"nope"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestHelpRecord(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run([]string{"help", "record"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !strings.Contains(stdout.String(), "localscribe record") {
		t.Fatalf("expected record usage in stdout")
	}
}

func TestHelpLast(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run([]string{"help", "last"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !strings.Contains(stdout.String(), "localscribe last") {
		t.Fatalf("expected last usage in stdout")
	}
}
