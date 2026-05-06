// Package requestlog writes one JSON line per terminal request to a daily-
// rotated file. Writes are buffered through a channel so the hot path isn't
// blocked on I/O.
package requestlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Record is one line in the log.
type Record struct {
	TS          time.Time `json:"ts"`
	Client      string    `json:"client,omitempty"` // name from access_tokens
	ClientToken string    `json:"client_token"`     // masked
	Provider    string    `json:"provider,omitempty"` // "anthropic" | "openai"; empty on legacy records
	AuthID      string    `json:"auth_id"`
	AuthLabel   string    `json:"auth_label,omitempty"`
	AuthKind    string    `json:"auth_kind"` // "oauth" or "apikey"
	Model       string    `json:"model"`
	Input       int64     `json:"input_tokens"`
	Output      int64     `json:"output_tokens"`
	CacheRead   int64     `json:"cache_read_tokens"`
	CacheCreate int64     `json:"cache_create_tokens"`
	CostUSD     float64   `json:"cost_usd"`
	Status      int       `json:"status"`
	DurationMs  int64     `json:"duration_ms"`
	Stream      bool      `json:"stream"`
	Path        string    `json:"path,omitempty"`
	Attempts    int       `json:"attempts,omitempty"` // credential attempts before terminal
	Error       string    `json:"error,omitempty"`
	// SaaS billing detail. UserID lets the /app/logs page filter to
	// just this account's requests; Multiplier records the per-(group ×
	// provider) coefficient that scaled the official cost into CostUSD,
	// so the user can verify each bill line against the catalog rate.
	// Zero/empty on requests that didn't go through the SaaS layer.
	UserID     int64   `json:"user_id,omitempty"`
	Multiplier float64 `json:"multiplier,omitempty"`
}

type Writer struct {
	dir           string
	retentionDays int
	ch            chan Record
	stopCh        chan struct{}
	doneCh        chan struct{}

	mu      sync.Mutex
	curFile *os.File
	curDay  string
}

// Open creates the writer, starts a background goroutine that drains the
// channel. dir will be created if missing. retentionDays <= 0 disables GC.
func Open(dir string, retentionDays int) (*Writer, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("requestlog: empty dir")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	w := &Writer{
		dir:           dir,
		retentionDays: retentionDays,
		ch:            make(chan Record, 1024),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	go w.loop()
	return w, nil
}

// Log enqueues a record. Non-blocking: if the buffer is full (slow disk,
// burst), the oldest pending entry is dropped to make room rather than
// blocking the hot path.
func (w *Writer) Log(r Record) {
	if w == nil {
		return
	}
	if r.TS.IsZero() {
		r.TS = time.Now()
	}
	select {
	case w.ch <- r:
	default:
		// Buffer full — drop one old entry and retry.
		select {
		case <-w.ch:
		default:
		}
		select {
		case w.ch <- r:
		default:
			// Still full; give up silently (one dropped line max).
		}
	}
}

// Close flushes pending entries, fsyncs and closes the current file.
// Safe to call multiple times.
func (w *Writer) Close() {
	if w == nil {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
		close(w.stopCh)
	}
	<-w.doneCh
}

func (w *Writer) loop() {
	defer close(w.doneCh)
	flushTicker := time.NewTicker(5 * time.Second)
	defer flushTicker.Stop()

	flush := func() {
		w.mu.Lock()
		if w.curFile != nil {
			_ = w.curFile.Sync()
		}
		w.mu.Unlock()
	}

	for {
		select {
		case <-w.stopCh:
			// Drain remaining.
			for {
				select {
				case r := <-w.ch:
					w.writeRecord(r)
				default:
					flush()
					w.closeFile()
					return
				}
			}
		case r := <-w.ch:
			w.writeRecord(r)
		case <-flushTicker.C:
			flush()
		}
	}
}

func (w *Writer) writeRecord(r Record) {
	day := r.TS.UTC().Format("2006-01-02")
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.curFile == nil || w.curDay != day {
		w.closeFileLocked()
		path := filepath.Join(w.dir, "requests-"+day+".jsonl")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			log.Errorf("requestlog: open %s: %v", path, err)
			return
		}
		w.curFile = f
		w.curDay = day
		go w.gc()
	}
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if _, err := w.curFile.Write(data); err != nil {
		log.Errorf("requestlog: write: %v", err)
	}
}

func (w *Writer) closeFileLocked() {
	if w.curFile != nil {
		_ = w.curFile.Sync()
		_ = w.curFile.Close()
		w.curFile = nil
		w.curDay = ""
	}
}

func (w *Writer) closeFile() {
	w.mu.Lock()
	w.closeFileLocked()
	w.mu.Unlock()
}

// gc deletes log files older than retentionDays. Runs on rotation (cheap).
func (w *Writer) gc() {
	if w.retentionDays <= 0 {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -w.retentionDays).Format("2006-01-02")
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "requests-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, "requests-"), ".jsonl")
		if day < cutoff {
			_ = os.Remove(filepath.Join(w.dir, name))
		}
	}
}
