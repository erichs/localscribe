package main

import (
	"time"
)

// heartbeat appends a timestamp line to the log file every minute, until ctx is done.
func heartbeat(cfg Config) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-cfg.Context.Done():
			return
		case <-ticker.C:
			timestamp := "%%% heartbeat " + time.Now().String() + "\n"
			atomicAppendToFile(cfg.LogFile, timestamp)
		}
	}
}
