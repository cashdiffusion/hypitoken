package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

// Disabling the archive AND the index leaves the request-log writer with
// nowhere to put a record. Starting up in that state would silently discard
// request history, so Load must refuse the pair outright.
func TestLoadRejectsArchiveAndIndexBothDisabled(t *testing.T) {
	_, err := loadYAML(t, "log_dir: /tmp/req\nlog_jsonl_disabled: true\nlog_index_disabled: true\n")
	if err == nil {
		t.Fatal("config with no archive and no index was accepted")
	}
	if !strings.Contains(err.Error(), "log_index_disabled") {
		t.Errorf("error %q does not name the knob to unset", err)
	}
}

func TestLoadAcceptsIndexOnly(t *testing.T) {
	cfg, err := loadYAML(t, "log_dir: /tmp/req\nlog_jsonl_disabled: true\n")
	if err != nil {
		t.Fatalf("index-only config rejected: %v", err)
	}
	if !cfg.LogJSONLDisabled || cfg.LogIndexDisabled {
		t.Fatalf("parsed wrong: jsonl_disabled=%v index_disabled=%v", cfg.LogJSONLDisabled, cfg.LogIndexDisabled)
	}
	// Retention has to survive the switch: with no files to expire, the index
	// prune is the only thing enforcing it, and 0 would mean "keep forever".
	if cfg.LogRetentionDays != 90 {
		t.Errorf("LogRetentionDays = %d, want the 90-day default", cfg.LogRetentionDays)
	}
}

// The historical default must stay: archive on, index on.
func TestLoadDefaultsKeepArchive(t *testing.T) {
	cfg, err := loadYAML(t, "log_dir: /tmp/req\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogJSONLDisabled {
		t.Error("archive off by default")
	}
	if cfg.LogIndexDisabled {
		t.Error("index off by default")
	}
}
