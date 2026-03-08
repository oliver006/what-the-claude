package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type sessionMeta struct {
	SessionID    string    `json:"session_id"`
	Started      time.Time `json:"started"`
	LastActivity time.Time `json:"last_activity"`
	RequestCount int       `json:"request_count"`
}

type storage struct {
	mu       sync.Mutex
	baseDir  string
	counters map[string]int
}

func newStorage(baseDir string) *storage {
	return &storage{
		baseDir:  baseDir,
		counters: make(map[string]int),
	}
}

func (s *storage) saveExchange(sessionID string, entry exchangeLog, now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.baseDir, sessionID)
	os.MkdirAll(dir, 0755)

	s.counters[sessionID]++
	counter := s.counters[sessionID]

	fname := fmt.Sprintf("%06d_%s.json", counter, now.UTC().Format("20060102T150405Z"))
	fpath := filepath.Join(dir, fname)

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		log.Printf("failed to marshal exchange: %v", err)
		return ""
	}
	if err := os.WriteFile(fpath, data, 0644); err != nil {
		log.Printf("failed to write exchange %s: %v", fpath, err)
		return ""
	}

	metaPath := filepath.Join(dir, "metadata.json")
	meta := sessionMeta{SessionID: sessionID, LastActivity: now, RequestCount: counter}

	existing, err := os.ReadFile(metaPath)
	if err == nil {
		var prev sessionMeta
		if json.Unmarshal(existing, &prev) == nil {
			meta.Started = prev.Started
		}
	}
	if meta.Started.IsZero() {
		meta.Started = now
	}

	metaData, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(metaPath, metaData, 0644)

	return fpath
}

func (s *storage) loadAllSessions() ([]*session, map[string]int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Printf("[storage.loadAllSessions] baseDir=%s", s.baseDir)

	sessions := []*session{}
	sessionIndex := map[string]int{}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		log.Printf("[storage.loadAllSessions] ReadDir error: %v", err)
		return sessions, sessionIndex
	}
	log.Printf("[storage.loadAllSessions] found %d dir entries", len(entries))

	for _, dirEntry := range entries {
		if !dirEntry.IsDir() {
			continue
		}
		sid := dirEntry.Name()
		dir := filepath.Join(s.baseDir, sid)

		metaPath := filepath.Join(dir, "metadata.json")
		metaData, err := os.ReadFile(metaPath)
		if err != nil {
			log.Printf("[storage.loadAllSessions] sid=%s: no metadata.json: %v", sid, err)
			continue
		}
		var meta sessionMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			log.Printf("[storage.loadAllSessions] sid=%s: bad metadata: %v", sid, err)
			continue
		}

		sess := &session{
			id:           sid,
			tag:          "",
			started:      meta.Started,
			lastActivity: meta.LastActivity,
		}

		files, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("[storage.loadAllSessions] sid=%s: ReadDir error: %v", sid, err)
			continue
		}

		var jsonFiles []string
		maxCounter := 0
		for _, f := range files {
			name := f.Name()
			if name == "metadata.json" || !strings.HasSuffix(name, ".json") {
				continue
			}
			jsonFiles = append(jsonFiles, name)
			if parts := strings.SplitN(name, "_", 2); len(parts) == 2 {
				if n, err := strconv.Atoi(parts[0]); err == nil && n > maxCounter {
					maxCounter = n
				}
			}
		}
		s.counters[sid] = maxCounter

		sort.Strings(jsonFiles)
		for _, name := range jsonFiles {
			fpath := filepath.Join(dir, name)
			data, err := os.ReadFile(fpath)
			if err != nil {
				log.Printf("[storage.loadAllSessions] sid=%s file=%s: read error: %v", sid, name, err)
				continue
			}
			var exch exchangeLog
			if err := json.Unmarshal(data, &exch); err != nil {
				log.Printf("[storage.loadAllSessions] sid=%s file=%s: unmarshal error: %v", sid, name, err)
				continue
			}

			ts, _ := time.Parse(time.RFC3339, exch.Timestamp)
			events := exch.Response.Events
			var model, sessionID string
			if exch.Request.Parsed != nil {
				model = exch.Request.Parsed.Model
				sessionID = exch.Request.Parsed.SessionID
			}

			use5h, _ := strconv.ParseFloat(exch.Anthropic["Ratelimit-Unified-5h-Utilization"], 64)
			use7d, _ := strconv.ParseFloat(exch.Anthropic["Ratelimit-Unified-7d-Utilization"], 64)
			reset5h, _ := strconv.ParseInt(exch.Anthropic["Ratelimit-Unified-5h-Reset"], 10, 64)
			reset7d, _ := strconv.ParseInt(exch.Anthropic["Ratelimit-Unified-7d-Reset"], 10, 64)

			le := logEntry{
				time:       ts,
				duration:   time.Duration(exch.DurationMs) * time.Millisecond,
				model:      model,
				status:     exch.Response.Status,
				events:     len(events),
				txBytes:    exch.TxBytes,
				rxBytes:    exch.RxBytes,
				totalTx:    exch.TotalTx,
				totalRx:    exch.TotalRx,
				use5h:      use5h,
				use7d:      use7d,
				reset5h:    reset5h,
				reset7d:    reset7d,
				reqBody:    string(exch.Request.Body),
				respBody:   string(exch.Response.Body),
				respEvents: events,
				anthropic:  exch.Anthropic,
				sessionID:  sessionID,
				filePath:   fpath,
			}
			sess.entries = append(sess.entries, le)
		}

		log.Printf("[storage.loadAllSessions] sid=%s: loaded %d entries", sid, len(sess.entries))
		idx := len(sessions)
		sessions = append(sessions, sess)
		sessionIndex[sid] = idx
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].lastActivity.After(sessions[j].lastActivity)
	})
	for i, sess := range sessions {
		sessionIndex[sess.id] = i
	}

	log.Printf("[storage.loadAllSessions] done: %d sessions total", len(sessions))
	return sessions, sessionIndex
}
