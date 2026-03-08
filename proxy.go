package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type exchangeLog struct {
	Timestamp  string            `json:"timestamp"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	DurationMs int64             `json:"duration_ms,omitempty"`
	Request    halfLog           `json:"request"`
	Response   respLog           `json:"response"`
	Anthropic  map[string]string `json:"anthropic"`
	TxBytes    int64             `json:"tx_bytes"`
	RxBytes    int64             `json:"rx_bytes"`
	TotalTx    int64             `json:"total_tx"`
	TotalRx    int64             `json:"total_rx"`
}

type parsedMetadata struct {
	Model     string `json:"model,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type halfLog struct {
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
	Parsed  *parsedMetadata   `json:"parsed,omitempty"`
}

type sseEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type respLog struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
	Events  []sseEvent        `json:"events,omitempty"`
}

type proxy struct {
	remote     *url.URL
	settings   Settings
	store      *storage
	txBytes    atomic.Int64
	rxBytes    atomic.Int64
	updateChan chan<- logEntry
}

func headerMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, vv := range h {
		m[k] = strings.Join(vv, ", ")
	}
	return m
}

func anthropicHeaders(h http.Header) map[string]string {
	m := make(map[string]string)
	for k, vv := range h {
		if strings.HasPrefix(strings.ToLower(k), "anthropic-") {
			short := k[len("Anthropic-"):]
			m[short] = strings.Join(vv, ", ")
		}
	}
	return m
}

func parseSSE(body []byte) []sseEvent {
	var events []sseEvent
	scanner := bufio.NewScanner(bytes.NewReader(body))
	var currentEvent string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			currentEvent = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			raw := after
			data := json.RawMessage(raw)
			if !json.Valid(data) {
				data, _ = json.Marshal(raw)
			}
			events = append(events, sseEvent{
				Event: currentEvent,
				Data:  data,
			})
			currentEvent = ""
		}
	}
	return events
}

func parseRequestBody(body []byte) *parsedMetadata {
	var raw struct {
		Model    string `json:"model"`
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	p := &parsedMetadata{Model: raw.Model}
	if uid := raw.Metadata.UserID; uid != "" {
		if _, after, ok := strings.Cut(uid, "user_"); ok {
			rest := after
			if j := strings.Index(rest, "_account_"); j >= 0 {
				p.UserID = rest[:j]
				rest = rest[j+len("_account_"):]
			} else {
				p.UserID = rest
				rest = ""
			}
			if rest != "" {
				if before, after, ok := strings.Cut(rest, "_session_"); ok {
					p.AccountID = before
					p.SessionID = after
				} else {
					p.AccountID = rest
				}
			}
		}
	}
	return p
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	var reqBody bytes.Buffer
	io.Copy(&reqBody, r.Body)
	r.Body.Close()

	target := *p.remote
	target.Path = r.URL.Path
	target.RawQuery = r.URL.RawQuery

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(reqBody.Bytes()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	outReq.Header = r.Header.Clone()
	outReq.Header.Del("Host")

	resp, err := http.DefaultClient.Do(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var respBody bytes.Buffer
	io.Copy(&respBody, resp.Body)

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody.Bytes())

	reqBodyJSON := json.RawMessage(reqBody.Bytes())
	if !json.Valid(reqBodyJSON) {
		reqBodyJSON, _ = json.Marshal(reqBody.String())
	}

	respBodyJSON := json.RawMessage(respBody.Bytes())
	if !json.Valid(respBodyJSON) {
		respBodyJSON, _ = json.Marshal(respBody.String())
	}

	now := time.Now()
	txN := int64(reqBody.Len())
	rxN := int64(respBody.Len())
	p.txBytes.Add(txN)
	p.rxBytes.Add(rxN)

	parsed := parseRequestBody(reqBody.Bytes())
	events := parseSSE(respBody.Bytes())

	var model, sessionID string
	if parsed != nil {
		model = parsed.Model
		sessionID = parsed.SessionID
	}
	use5h, _ := strconv.ParseFloat(resp.Header.Get("Anthropic-Ratelimit-Unified-5h-Utilization"), 64)
	use7d, _ := strconv.ParseFloat(resp.Header.Get("Anthropic-Ratelimit-Unified-7d-Utilization"), 64)
	reset5h, _ := strconv.ParseInt(resp.Header.Get("Anthropic-Ratelimit-Unified-5h-Reset"), 10, 64)
	reset7d, _ := strconv.ParseInt(resp.Header.Get("Anthropic-Ratelimit-Unified-7d-Reset"), 10, 64)

	duration := time.Since(startTime)
	entry := exchangeLog{
		Timestamp:  now.Format(time.RFC3339),
		Method:     r.Method,
		Path:       r.URL.Path,
		DurationMs: duration.Milliseconds(),
		Request: halfLog{
			Headers: headerMap(r.Header),
			Body:    reqBodyJSON,
			Parsed:  parsed,
		},
		Response: respLog{
			Status:  resp.StatusCode,
			Headers: headerMap(resp.Header),
			Body:    respBodyJSON,
			Events:  events,
		},
		Anthropic: anthropicHeaders(resp.Header),
		TxBytes:   txN,
		RxBytes:   rxN,
		TotalTx:   p.txBytes.Load(),
		TotalRx:   p.rxBytes.Load(),
	}

	var filePath string
	if p.settings.Capture && parsed != nil && parsed.SessionID != "" {
		filePath = p.store.saveExchange(parsed.SessionID, entry, now)
	}

	p.updateChan <- logEntry{
		time:       now,
		duration:   duration,
		model:      model,
		status:     resp.StatusCode,
		events:     len(events),
		txBytes:    txN,
		rxBytes:    rxN,
		totalTx:    p.txBytes.Load(),
		totalRx:    p.rxBytes.Load(),
		use5h:      use5h,
		use7d:      use7d,
		reset5h:    reset5h,
		reset7d:    reset7d,
		reqBody:    string(reqBodyJSON),
		respBody:   string(respBodyJSON),
		respEvents: events,
		anthropic:  anthropicHeaders(resp.Header),
		sessionID:  sessionID,
		filePath:   filePath,
	}
}
