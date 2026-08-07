package main

import (
	"strings"
	"testing"

	"github.com/wjsoj/cc-core/backup"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// A backup that ships without the wallet DB or without the credential files is
// worse than no backup, because it looks like one: the archive encrypts and
// restores cleanly, the upload logs success, and the systemd oneshot goes
// green. These tests pin the guard that turns that into a loud failure.

func manifest(names ...string) []backup.FileEntry {
	out := make([]backup.FileEntry, 0, len(names))
	for _, n := range names {
		out = append(out, backup.FileEntry{Name: n})
	}
	return out
}

func fullConfig() *config.Config {
	cfg := &config.Config{AuthDir: "/var/lib/app/auths"}
	cfg.SaaS.Enabled = true
	cfg.Shop.Enabled = true
	return cfg
}

func TestManifestCompleteAcceptsFullArchive(t *testing.T) {
	err := assertManifestComplete(fullConfig(), manifest(
		"saas.db", "shop.db", "tokens.json", "config.yaml", "state.json",
		"auths/a.json", "auths/b.json",
	))
	if err != nil {
		t.Fatalf("complete manifest rejected: %v", err)
	}
}

func TestManifestCompleteRejectsMissingPieces(t *testing.T) {
	cases := []struct {
		name    string
		entries []backup.FileEntry
		want    string
	}{
		{
			// The exact shape seen in a drill: a config whose relative paths
			// all missed, leaving one file in the archive.
			name:    "everything missed",
			entries: manifest("config.yaml"),
			want:    "saas.db",
		},
		{
			name:    "wallet db missing",
			entries: manifest("shop.db", "auths/a.json"),
			want:    "saas.db",
		},
		{
			name:    "shop db missing",
			entries: manifest("saas.db", "auths/a.json"),
			want:    "shop.db",
		},
		{
			// Credentials are the only unrecoverable content in the archive.
			name:    "no credentials",
			entries: manifest("saas.db", "shop.db", "tokens.json"),
			want:    "auth_dir",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := assertManifestComplete(fullConfig(), tc.entries)
			if err == nil {
				t.Fatal("incomplete manifest accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestManifestCompleteIgnoresDisabledComponents(t *testing.T) {
	// A deployment with no shop must not be forced to ship a shop.db.
	cfg := &config.Config{AuthDir: "/var/lib/app/auths"}
	cfg.SaaS.Enabled = true
	if err := assertManifestComplete(cfg, manifest("saas.db", "auths/a.json")); err != nil {
		t.Fatalf("shop disabled but still required: %v", err)
	}
	// And a proxy-only deployment (no SaaS, no shop) only needs credentials.
	bare := &config.Config{AuthDir: "/var/lib/app/auths"}
	if err := assertManifestComplete(bare, manifest("auths/a.json", "config.yaml")); err != nil {
		t.Fatalf("bare deployment rejected: %v", err)
	}
}
