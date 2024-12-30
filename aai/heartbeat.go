package main

import (
	"fmt"
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
			heartbeat := fmt.Sprintf("%s %s\n", time.Now().Format("2006/01/02 15:04:05"), "%%% heartbeat")
			atomicAppendToFile(cfg.LogFile, heartbeat)
		}
	}
}
