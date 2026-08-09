package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/cc-core/requestlog"
)

// runExportRequestsCmd implements `<binary> export-requests`.
//
// It exists to make log_jsonl_disabled a reversible decision. With the archive
// on, the .jsonl files are the export; with it off, this is the only way to get
// request history back into a shape that greps, diffs, or feeds another tool.
// Output is the identical line format, so an exported file can simply be
// dropped back into a log directory.
//
// Unlike the sibling fork this does NOT call requestlog.SetBucketLocation: this
// server never sets it either, so the index's day labels are in cc-core's
// default zone and the export has to agree with the server, not with a config
// knob that does not exist here.
func runExportRequestsCmd(args []string) {
	fs := flag.NewFlagSet("export-requests", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config file")
	from := fs.String("from", "", "earliest day to include, YYYY-MM-DD; empty = no bound")
	to := fs.String("to", "", "latest day to include, YYYY-MM-DD; empty = no bound")
	out := fs.String("out", "-", "output file, or - for stdout")
	_ = fs.Parse(args)

	for _, d := range []struct{ name, val string }{{"from", *from}, {"to", *to}} {
		if d.val == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d.val); err != nil {
			fmt.Fprintf(os.Stderr, "export-requests: --%s must be YYYY-MM-DD: %v\n", d.name, err)
			os.Exit(2)
		}
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	if cfg.LogDir == "" {
		fmt.Fprintln(os.Stderr, "export-requests: log_dir is not configured")
		os.Exit(2)
	}

	// Read-only, and deliberately so: this normally runs on a box where the
	// server has the same database open, and the read-write OpenStore would
	// start a second ingest loop competing with it.
	st, err := requestlog.OpenStoreForRead(cfg.LogDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "export-requests: open index: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	w := os.Stdout
	if *out != "-" {
		f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "export-requests: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	bw := bufio.NewWriterSize(w, 256*1024)
	n, err := st.Export(*from, *to, bw)
	if flushErr := bw.Flush(); err == nil {
		err = flushErr
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "export-requests: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "export-requests: wrote %d records\n", n)
}
