package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// IPInfo captures just the fields we're interested in.
type IPInfo struct {
	IP     string `json:"ip"`
	City   string `json:"city"`
	Region string `json:"region"`
	Org    string `json:"org"`
}

func fetchIPInfo() string {
	resp, err := http.Get("https://ipinfo.io")
	if err != nil {
		log.Fatalf("Failed to retrieve IP info: %v", err)
	}
	defer resp.Body.Close()

	var info IPInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		log.Fatalf("Failed to decode JSON: %v", err)
	}

	return fmt.Sprintf("ip: %s, near: %s, %s, isp: %s\n", info.IP, info.City, info.Region, info.Org)
}
