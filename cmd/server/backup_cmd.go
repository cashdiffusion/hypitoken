package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/cc-core/backup"
	"github.com/wjsoj/cc-core/requestlog"
)

// defaultPrefix namespaces this app's objects inside the shared bucket.
const defaultBackupPrefix = "hypitoken/"

// runBackupCmd implements `<binary> backup` (and `backup keygen`). It snapshots
// the SQLite DBs, gathers the critical-file manifest, and ships an encrypted
// archive off-host. Exits non-zero on failure so the systemd oneshot reports it.
func runBackupCmd(args []string) {
	if len(args) > 0 && args[0] == "keygen" {
		pub, priv, err := backup.GenerateKeypair()
		if err != nil {
			fmt.Fprintf(os.Stderr, "keygen: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("# Backup keypair — put the PUBLIC key in config (backup.recipient_pubkey),\n")
		fmt.Printf("# and store the PRIVATE key OFFLINE (needed only for restore; never on the server).\n")
		fmt.Printf("public  (recipient_pubkey): %s\n", pub)
		fmt.Printf("private (KEEP OFFLINE):      %s\n", priv)
		return
	}

	if len(args) > 0 && args[0] == "list" {
		runBackupListCmd(args[1:])
		return
	}

	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config file")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	if !cfg.Backup.Enabled {
		// Clean no-op so the daily systemd timer doesn't fail on installs that
		// haven't configured off-host backup yet.
		log.Info("backup: disabled (set backup.enabled: true to enable off-host backup) — nothing to do")
		return
	}

	if err := runBackup(cfg, *configPath); err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
}

// runBackup snapshots → archives → uploads. It returns errors instead of
// calling os.Exit so the deferred tmp-dir cleanup always runs (gocritic
// exitAfterDefer); runBackupCmd maps a non-nil error to a non-zero exit.
func runBackup(cfg *config.Config, configPath string) error {
	opt, err := backupOptions(cfg)
	if err != nil {
		return err
	}
	// System temp (not the config dir — that may be root-owned while the
	// backup runs as a less-privileged service user). PrivateTmp on the unit
	// keeps it isolated.
	tmpDir, err := os.MkdirTemp("", "hypitoken-backup-")
	if err != nil {
		return fmt.Errorf("tmp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	entries, err := buildManifest(context.Background(), cfg, configPath, tmpDir)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	key, err := backup.RunBackup(context.Background(), opt, entries)
	if err != nil {
		return err
	}
	log.Infof("backup: uploaded %d files → s3://%s/%s", len(entries), opt.S3.Bucket, key)
	return nil
}

// runRestoreCmd implements `<binary> restore` for disaster recovery. The
// identity (private key) and, optionally, the read-only S3 credentials are
// supplied at the command line so they never need to live in a persisted
// config on the recovered host.
func runRestoreCmd(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config file (for S3 target)")
	date := fs.String("date", "latest", "backup date YYYY-MM-DD, or 'latest'")
	identityFile := fs.String("identity", "", "path to the offline private key file ('-' for stdin)")
	dest := fs.String("dest", "", "destination dir (default: config dir)")
	s3KeyID := fs.String("s3-access-key-id", "", "override S3 access key id (use the offline restore key)")
	s3Secret := fs.String("s3-secret-key", "", "override S3 secret key ('@/path' supported)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	s3, err := backupS3(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(*s3KeyID) != "" {
		s3.AccessKeyID = *s3KeyID
	}
	if strings.TrimSpace(*s3Secret) != "" {
		if v, err := loadKeyFile(*s3Secret); err == nil {
			s3.SecretAccessKey = v
		}
	}
	identity, err := readIdentity(*identityFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore: identity: %v\n", err)
		os.Exit(1)
	}
	out := strings.TrimSpace(*dest)
	if out == "" {
		out = filepath.Dir(*configPath)
	}
	if err := backup.Restore(context.Background(), s3, identity, *date, out); err != nil {
		fmt.Fprintf(os.Stderr, "restore: %v\n", err)
		os.Exit(1)
	}
	log.Infof("restore: extracted backup (%s) → %s", *date, out)
	log.Infof("restore: NOTE check external/MANIFEST.json for payment secrets that belong outside the config dir")
}

func readIdentity(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("--identity is required (the offline private key)")
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func backupOptions(cfg *config.Config) (backup.Options, error) {
	s3, err := backupS3(cfg)
	if err != nil {
		return backup.Options{}, err
	}
	pub := strings.TrimSpace(cfg.Backup.RecipientPubKey)
	if pub == "" {
		return backup.Options{}, fmt.Errorf("backup.recipient_pubkey is empty (run `backup keygen`)")
	}
	return backup.Options{
		S3:              s3,
		RecipientPubKey: pub,
		RetentionDays:   cfg.Backup.RetentionDays,
	}, nil
}

func backupS3(cfg *config.Config) (backup.S3Config, error) {
	s := cfg.Backup.S3
	id, err := loadKeyFile(s.AccessKeyID)
	if err != nil {
		return backup.S3Config{}, fmt.Errorf("s3 access_key_id: %w", err)
	}
	secret, err := loadKeyFile(s.SecretAccessKey)
	if err != nil {
		return backup.S3Config{}, fmt.Errorf("s3 secret_access_key: %w", err)
	}
	prefix := strings.TrimSpace(s.Prefix)
	if prefix == "" {
		prefix = defaultBackupPrefix
	}
	return backup.S3Config{
		Endpoint:        s.Endpoint,
		Region:          s.Region,
		Bucket:          s.Bucket,
		Prefix:          prefix,
		AccessKeyID:     id,
		SecretAccessKey: secret,
	}, nil
}

// buildManifest collects every file that must survive a total server loss.
// SQLite DBs are snapshotted (VACUUM INTO) into tmpDir for a consistent,
// WAL-free copy. Missing optional files are skipped silently; missing
// critical files (saas.db / tokens.json) log a warning.
func buildManifest(ctx context.Context, cfg *config.Config, configPath, tmpDir string) ([]backup.FileEntry, error) {
	var entries []backup.FileEntry
	add := func(name, src string, mode os.FileMode) {
		if fileExists(src) {
			entries = append(entries, backup.FileEntry{Name: name, SourcePath: src, Mode: mode})
		}
	}

	// SQLite snapshots (consistent, online).
	if cfg.SaaS.Enabled && fileExists(cfg.SaaS.DBPath) {
		snap := filepath.Join(tmpDir, "saas.db")
		if err := backup.SnapshotSQLite(ctx, cfg.SaaS.DBPath, snap); err != nil {
			return nil, fmt.Errorf("snapshot saas.db: %w", err)
		}
		entries = append(entries, backup.FileEntry{Name: "saas.db", SourcePath: snap, Mode: 0o600})
		add("saas.db.jwt_secret", cfg.SaaS.DBPath+".jwt_secret", 0o600)
	} else {
		log.Warn("backup: saas.db not present/enabled — skipping (no wallet data to back up?)")
	}
	if cfg.Shop.Enabled && fileExists(cfg.Shop.DBPath) {
		snap := filepath.Join(tmpDir, "shop.db")
		if err := backup.SnapshotSQLite(ctx, cfg.Shop.DBPath, snap); err != nil {
			return nil, fmt.Errorf("snapshot shop.db: %w", err)
		}
		entries = append(entries, backup.FileEntry{Name: "shop.db", SourcePath: snap, Mode: 0o600})
	}

	// Request history. With log_jsonl_disabled this database is the ONLY copy:
	// there is no requests-*.jsonl left to rebuild it from, so a disk loss
	// would take the whole retention window of billing and audit history with
	// it. It goes in either way — while the archive exists the .jsonl files
	// are not backed up either, so the index is the only form of that history
	// this archive can carry.
	//
	// Snapshotted, not copied: the server holds it open in WAL mode, and a raw
	// copy without the -wal sibling is a torn read. VACUUM INTO also compacts,
	// so the snapshot tracks live rows rather than the file's high-water mark.
	if err := addRequestLogDB(ctx, cfg, tmpDir, &entries); err != nil {
		return nil, err
	}

	// Identity + config.
	tokensPath := filepath.Join(filepath.Dir(cfg.StateFile), "tokens.json")
	if fileExists(tokensPath) {
		entries = append(entries, backup.FileEntry{Name: "tokens.json", SourcePath: tokensPath, Mode: 0o600})
	} else {
		log.Warn("backup: tokens.json not present — skipping")
	}
	add("config.yaml", configPath, 0o600)

	// Upstream credential dirs (refresh_tokens — unrecoverable if lost).
	entries = append(entries, dirEntries(cfg.AuthDir, "auths")...)

	// Usage state: per-credential day/hour counters and per-client-token
	// weekly spend. Money lives in saas.db, so losing this doesn't lose
	// revenue — but it does reset every client's weekly-budget counter to
	// zero, which lets a capped token overspend for a week. The sibling
	// CPA-Claude fork has always captured it; this side just never did.
	add("state.json", cfg.StateFile, 0o600)

	// In-config-dir secrets/ folder (if used).
	configDir := filepath.Dir(configPath)
	entries = append(entries, dirEntries(filepath.Join(configDir, "secrets"), "secrets")...)

	// External @path secrets (payment private keys etc. that live outside the
	// config dir, e.g. @/etc/hypitoken/stripe_secret_key). Captured under
	// external/ with a manifest mapping basename → original absolute path.
	ext, err := externalSecretEntries(cfg, configDir, tmpDir)
	if err != nil {
		return nil, err
	}
	entries = append(entries, ext...)

	if err := assertManifestComplete(cfg, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// addRequestLogDB appends a snapshot of the request-log index, if there is one.
//
// A missing file is not an error: log_dir may be unset, or the index may be
// disabled, or the server may simply not have created it yet. What IS an error
// is failing to snapshot a database that exists — see assertManifestComplete
// for the case where its absence is fatal.
func addRequestLogDB(ctx context.Context, cfg *config.Config, tmpDir string, entries *[]backup.FileEntry) error {
	dir := strings.TrimSpace(cfg.LogDir)
	if dir == "" {
		return nil
	}
	src := filepath.Join(dir, requestlog.IndexFileName)
	if !fileExists(src) {
		return nil
	}
	snap := filepath.Join(tmpDir, requestlog.IndexFileName)
	if err := backup.SnapshotSQLite(ctx, src, snap); err != nil {
		return fmt.Errorf("snapshot %s: %w", requestlog.IndexFileName, err)
	}
	*entries = append(*entries, backup.FileEntry{Name: requestlog.IndexFileName, SourcePath: snap, Mode: 0o600})
	return nil
}

// assertManifestComplete fails the run when something the config says should
// exist did not make it into the archive.
//
// The old guard was `len(entries) == 0`. That is far too weak: point a config
// at the wrong directory and every collector silently skips, the archive ships
// with one file, the upload logs "uploaded 1 files", and the systemd oneshot
// reports success — a backup that exists, is encrypted, restores cleanly, and
// contains nothing. (Observed while drill-testing: a config copied to /tmp made
// every relative path miss, and the run still "succeeded".) Better to fail
// loudly, because a failed unit is visible and an empty archive is not.
func assertManifestComplete(cfg *config.Config, entries []backup.FileEntry) error {
	have := make(map[string]bool, len(entries))
	auths := 0
	for _, e := range entries {
		have[e.Name] = true
		if strings.HasPrefix(e.Name, "auths/") {
			auths++
		}
	}
	var missing []string
	if cfg.SaaS.Enabled && !have["saas.db"] {
		missing = append(missing, "saas.db (saas.enabled is true)")
	}
	if cfg.Shop.Enabled && !have["shop.db"] {
		missing = append(missing, "shop.db (shop.enabled is true)")
	}
	// With the JSONL archive off the index is request history's only copy, so
	// shipping without it is exactly the silent-empty-archive failure this
	// function exists to prevent. While the archive is on its absence is
	// tolerable: the .jsonl files on disk can still rebuild it.
	if cfg.LogDir != "" && cfg.LogJSONLDisabled && !have[requestlog.IndexFileName] {
		missing = append(missing, fmt.Sprintf("%s (log_jsonl_disabled is true, so it is the only copy of request history)",
			requestlog.IndexFileName))
	}
	// An empty auth dir means the credential files — the one unrecoverable
	// thing in here — are not in the archive.
	if strings.TrimSpace(cfg.AuthDir) != "" && auths == 0 {
		missing = append(missing, fmt.Sprintf("any credential from auth_dir %q", cfg.AuthDir))
	}
	if len(missing) > 0 {
		return fmt.Errorf("refusing to ship an incomplete backup — missing %s; check the paths in this config resolve",
			strings.Join(missing, ", "))
	}
	if len(entries) == 0 {
		return fmt.Errorf("nothing to back up")
	}
	return nil
}

// externalSecretEntries finds config string fields that use the "@/path"
// convention pointing OUTSIDE the config dir, copies each into tmpDir, and
// returns FileEntries under "external/" plus an "external/MANIFEST.json"
// mapping archive name → original absolute path (for restore placement).
func externalSecretEntries(cfg *config.Config, configDir, tmpDir string) ([]backup.FileEntry, error) {
	candidates := []string{
		cfg.SaaS.ZPay.Key,
		cfg.SaaS.Stripe.SecretKey, cfg.SaaS.Stripe.WebhookSecret,
		cfg.SaaS.SMTP.Password,
		cfg.Shop.Stripe.SecretKey, cfg.Shop.Stripe.WebhookSecret,
	}
	manifest := map[string]string{}
	var entries []backup.FileEntry
	seen := map[string]bool{}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if !strings.HasPrefix(c, "@") {
			continue
		}
		abs := c[1:]
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(configDir, abs)
		}
		if !fileExists(abs) || seen[abs] {
			continue
		}
		// Always include @path secrets — they're loose files (often in the
		// config dir root, e.g. /etc/hypitoken/stripe_secret_key) that no other
		// glob captures. The external/ manifest records the original path so
		// restore can place them back.
		seen[abs] = true
		name := "external/" + filepath.Base(abs)
		for i := 1; manifest[name] != ""; i++ {
			name = fmt.Sprintf("external/%d_%s", i, filepath.Base(abs))
		}
		manifest[name] = abs
		entries = append(entries, backup.FileEntry{Name: name, SourcePath: abs, Mode: 0o600})
	}
	if len(manifest) > 0 {
		mf := filepath.Join(tmpDir, "external-manifest.json")
		b, _ := json.MarshalIndent(manifest, "", "  ")
		if err := os.WriteFile(mf, b, 0o600); err != nil {
			return nil, err
		}
		entries = append(entries, backup.FileEntry{Name: "external/MANIFEST.json", SourcePath: mf, Mode: 0o600})
	}
	return entries, nil
}

// dirEntries globs the immediate files in dir and returns FileEntries named
// "<prefix>/<file>". Subdirectories are skipped. Missing dir = no entries.
func dirEntries(dir, prefix string) []backup.FileEntry {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []backup.FileEntry
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		out = append(out, backup.FileEntry{
			Name:       prefix + "/" + e.Name(),
			SourcePath: filepath.Join(dir, e.Name()),
			Mode:       0o600,
		})
	}
	return out
}

func fileExists(p string) bool {
	if strings.TrimSpace(p) == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
