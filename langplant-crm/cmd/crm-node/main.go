// crm-node is the storage service of the LangPlant CRM. It runs on the PC with
// the disks (normally in Docker), connects out to the VPS over WebSocket and
// keeps every uploaded file.
//
//	crm-node                     run (configured via NODE_* env vars)
//	crm-node export [--db f] [--out dir] [--copy]
//	                             rebuild a readable folder tree from a DB backup
//	crm-node verify              re-hash all stored files
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"langplant-crm/internal/config"
	"langplant-crm/internal/node"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if len(os.Args) > 1 {
		dataDir := os.Getenv("NODE_DATA_DIR")
		if dataDir == "" {
			dataDir = "./storage"
		}
		switch os.Args[1] {
		case "export":
			fs := flag.NewFlagSet("export", flag.ExitOnError)
			dbPath := fs.String("db", "", "database backup (default: the newest in <data>/backups)")
			out := fs.String("out", dataDir+"/export", "output directory")
			copyFiles := fs.Bool("copy", false, "copy files instead of hard-linking")
			fs.Parse(os.Args[2:])
			if err := node.Export(dataDir, *dbPath, *out, *copyFiles); err != nil {
				fatal(err)
			}
			return
		case "verify":
			if err := node.Verify(dataDir); err != nil {
				fatal(err)
			}
			return
		case "run":
		default:
			slog.Error("unknown command", "cmd", os.Args[1])
			os.Exit(2)
		}
	}

	cfg, err := config.LoadNode()
	if err != nil {
		fatal(err)
	}
	n, err := node.New(cfg)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	slog.Info("crm-node starting", "version", node.Version, "server", cfg.ServerURL, "data", cfg.DataDir)
	n.Run(ctx)
	slog.Info("crm-node stopped")
}

func fatal(err error) {
	slog.Error(err.Error())
	os.Exit(1)
}
