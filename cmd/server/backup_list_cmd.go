package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/cc-core/backup"
)

// runBackupListCmd implements `<binary> backup list`.
//
// It exists because there was no way to see what the bucket actually held
// without reaching for an S3 client and hand-writing a request — which is a
// poor position to be in during an incident, exactly when someone needs to
// know whether a usable copy is up there before deciding what to do next.
//
// Read-only, and needs no private key: sizes and names are enough to tell a
// full archive from a thin one.
func runBackupListCmd(args []string) {
	fs := flag.NewFlagSet("backup list", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config file")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	s3, err := backupS3(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup list: %v\n", err)
		os.Exit(1)
	}
	objs, err := backup.ListBackups(context.Background(), s3)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup list: %v\n", err)
		os.Exit(1)
	}
	if len(objs) == 0 {
		fmt.Fprintln(os.Stderr, "backup list: no backups found")
		return
	}
	fmt.Printf("%-42s  %10s  %s\n", "KEY", "SIZE", "TAKEN (UTC)")
	for _, o := range objs {
		note := ""
		if o.Legacy {
			// Worth flagging: these predate timestamped keys, so a same-day
			// rerun could have replaced whatever was there before.
			note = "  (legacy one-per-day key)"
		}
		fmt.Printf("%-42s  %7.1f MB  %s%s\n",
			o.Key, float64(o.Size)/(1<<20), o.Date.UTC().Format(time.RFC3339), note)
	}
	fmt.Fprintf(os.Stderr, "\n%d backup(s). Restore one with: restore --date <YYYY-MM-DD|stem> --identity <key file>\n", len(objs))
}
