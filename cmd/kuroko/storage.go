package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ryu/kuroko/internal/config"
	"github.com/ryu/kuroko/internal/logstore"
	"github.com/ryu/kuroko/internal/migration"
	"github.com/ryu/kuroko/internal/viewer"
)

func runLogs(args []string, logDir string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, "usage: kuroko logs [--info]") }
	info := fs.Bool("info", false, "show storage root, counts, sizes and groups")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: kuroko logs [--info]")
	}
	cfg, err := config.Resolve(config.Options{LogDir: logDir})
	if err != nil {
		return err
	}
	if *info {
		return logstore.WriteInfo(stdout, cfg.LogDir)
	}
	return viewer.RunSelector(cfg)
}

func runMigration(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: kuroko migrate [--from /absolute/log/directory]\nCopies legacy logs and config to ~/.kuroko, verifies SHA-256 checksums, then removes verified sources.")
	}
	from := fs.String("from", "", "override the legacy log root")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: kuroko migrate [--from /absolute/log/directory]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	r, err := migration.Run(home, *from)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Migration verified: %d logs, %d files, %d bytes\nStorage: %s\n", r.Logs, r.Files, r.Bytes, r.Destination)
	if r.ConfigMigrated {
		fmt.Fprintln(stdout, "Config: migrated and verified; legacy config removed")
	}
	if r.SourceRemaining {
		fmt.Fprintln(stdout, "Legacy directory contains other files and was retained")
	}
	return nil
}
