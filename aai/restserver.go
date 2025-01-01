package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
)

// startRESTServer spins up an HTTP server on cfg.RESTPort (default 8080).
// It provides two GET endpoints:
//
//	/command?body=some command text
//	/metadata?body=some metadata
//
// Each appends lines to cfg.LogFile: "### ..." or "%%% ...", respectively.
//
// The server runs until cfg.Context is canceled. On cancel(), it shuts down cleanly.
func startRESTServer(cfg Config) error {
	// Decide which port to use; default to 8080 if not set or zero.
	port := 8080
	if cfg.RESTPort != 0 {
		port = cfg.RESTPort
	}

	mux := http.NewServeMux()

	// /command?body=...
	mux.HandleFunc("/command", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Only GET supported", http.StatusMethodNotAllowed)
			return
		}
		body := r.URL.Query().Get("body")
		if body == "" {
			http.Error(w, "Missing query param 'body'", http.StatusBadRequest)
			return
		}
		line := fmt.Sprintf("%s ### %s", getDateTime(), body)

		if err := atomicAppendToFile(cfg.LogFile, line); err != nil {
			log.Printf("Error appending command line: %v\n", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "OK: appended command => %s\n", line)
	})

	// /metadata?body=...
	mux.HandleFunc("/metadata", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Only GET supported", http.StatusMethodNotAllowed)
			return
		}
		body := r.URL.Query().Get("body")
		if body == "" {
			http.Error(w, "Missing query param 'body'", http.StatusBadRequest)
			return
		}
		line := fmt.Sprintf("%s %%%%%% %s", getDateTime(), body)

		if err := atomicAppendToFile(cfg.LogFile, line); err != nil {
			log.Printf("Error appending metadata line: %v\n", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "OK: appended metadata => %s\n", line)
	})

	// Create an http.Server that we'll start in a goroutine
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	// Start serving in a separate goroutine.
	go func() {
		log.Printf("start web server listening on :%d ...", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("web server error: %v\n", err)
		}
	}()

	// Block until cfg.Context is canceled.
	<-cfg.Context.Done()

	// A short timeout context to allow any in-flight requests to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("web server shutdown error: %v\n", err)
		return err
	}

	log.Println("web server shut down")
	return nil
}
