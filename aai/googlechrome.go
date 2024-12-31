// googlechrome.go
package main

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3" // or another SQLite driver
)

// startChromeHistoryPolling spawns a goroutine that every 10 minutes:
// 1) Copies Chrome's locked History DB to /tmp
// 2) Queries recent visits in last 10 minutes
// 3) Appends them as metadata lines to cfg.LogFile
// 4) Cleans up after itself
func startChromeHistoryPolling(cfg Config) error {
	ticker := time.NewTicker(1 * time.Minute)
	log.Println("google chrome history polling started.")
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-cfg.Context.Done():
				log.Println("google chrome history polling stopped")
				return
			case <-ticker.C:
				if err := pollChromeHistory(cfg); err != nil {
					log.Printf("Chrome history error: %v\n", err)
				}
			}
		}
	}()
	return nil
}

// pollChromeHistory does the actual copying, querying, and log emission.
func pollChromeHistory(cfg Config) error {
	// The path to Chrome's primary "History" file on macOS
	srcPath := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Google", "Chrome", "Default", "History")
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("History_copy_%d", time.Now().UnixNano()))

	if err := copyFile(srcPath, tmpPath); err != nil {
		return fmt.Errorf("failed to copy History db: %w", err)
	}
	defer os.Remove(tmpPath) // clean up after we query

	// Now open the temp copy read-only
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", tmpPath))
	if err != nil {
		return fmt.Errorf("failed to open temp History db: %w", err)
	}
	defer db.Close()

	// Query for visits in last 10 minutes
	// Chrome timestamps in microseconds since 1601-01-01
	// We'll convert them to local time with datetime( ... ).
	query := `
SELECT
  urls.url,
  urls.title,
  datetime((visits.visit_time/1000000) - 11644473600, 'unixepoch', 'localtime') as visited_local
FROM urls
JOIN visits ON urls.id = visits.url
WHERE
  visits.visit_time > (
    (strftime('%s','now','-10 minutes') + 11644473600) * 1000000
  )
ORDER BY visits.visit_time ASC
`

	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("failed to query temp History db: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var chromeURL, title, visitedLocal string
		if err := rows.Scan(&chromeURL, &title, &visitedLocal); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}

		// Build the line for the localscribe log
		// e.g. %%% chrome url: https://..., title: "Foo", visited: YYYY-MM-DD HH:MM:SS

		line := fmt.Sprintf("%s %s url: %s, title: %q, visited: %s", time.Now().Format("2006/01/02 15:04:05"), "%%% google chrome", chromeURL, title, visitedLocal)
		if err := atomicAppendToFile(cfg.LogFile, line); err != nil {
			log.Printf("failed to append line to log: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("rows iteration error: %v", err)
	}

	return nil
}

// copyFile performs a simple file copy, needed because Chrome keeps the original locked while running.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = dstFile.Close()
	}()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	return nil
}
