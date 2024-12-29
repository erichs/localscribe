package main

import (
	"context"
	"time"
)

// heartbeat appends a timestamp line to the log file every minute, until ctx is done.
func heartbeat(ctx context.Context, cfg Config) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			timestamp := "%%% heartbeat " + time.Now().String() + "\n"
			atomicAppendToFile(cfg.LogFile, timestamp)
		}
	}
}
