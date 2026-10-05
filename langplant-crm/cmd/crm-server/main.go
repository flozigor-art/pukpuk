// crm-server runs the LangPlant CRM on the VPS: web UI, API, upload buffer and
// the endpoint the storage node connects to.
//
//	crm-server                         start the server (configured via CRM_* env vars)
//	crm-server passwd <login> [pass]   set a password (random if omitted)
//	crm-server useradd <login> <name> [admin]
//	crm-server users                   list accounts
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"langplant-crm/internal/config"
	"langplant-crm/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	cfg, err := config.LoadServer()
	if err != nil {
		fatal(err)
	}
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		if err := runCommand(cfg, os.Args[1:]); err != nil {
			fatal(err)
		}
		return
	}
	if cfg.NodeToken == "" {
		slog.Warn("CRM_NODE_TOKEN is empty: the storage node cannot connect and uploads will stay in the VPS buffer")
	}

	srv, err := server.New(cfg)
	if err != nil {
		fatal(err)
	}
	srv.Start()
	hs := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "version", server.Version, "data", cfg.DataDir)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hs.Shutdown(ctx)
	srv.Close()
}

func runCommand(cfg config.Server, args []string) error {
	srv, err := server.New(cfg)
	if err != nil {
		return err
	}
	defer srv.Close()
	db := srv.DB()
	switch args[0] {
	case "users":
		rows, err := db.Query(`SELECT login, name, role, disabled FROM users ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var login, name, role string
			var disabled bool
			rows.Scan(&login, &name, &role, &disabled)
			state := ""
			if disabled {
				state = " (заблокирован)"
			}
			fmt.Printf("%-12s %-14s %s%s\n", login, name, role, state)
		}
		return nil
	case "passwd":
		if len(args) < 2 {
			return errors.New("usage: crm-server passwd <login> [password]")
		}
		pw := ""
		if len(args) > 2 {
			pw = args[2]
		} else {
			pw = server.GeneratePassword()
		}
		if len(pw) < 8 {
			return errors.New("password must be at least 8 characters")
		}
		hash, err := server.HashPassword(pw)
		if err != nil {
			return err
		}
		res, err := db.Exec(`UPDATE users SET password_hash = ?, disabled = 0 WHERE login = ?`, hash, args[1])
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("no user %q", args[1])
		}
		db.Exec(`DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE login = ?)`, args[1])
		fmt.Printf("%s: %s\n", args[1], pw)
		return nil
	case "useradd":
		if len(args) < 3 {
			return errors.New("usage: crm-server useradd <login> <name> [admin]")
		}
		role := "member"
		if len(args) > 3 && args[3] == "admin" {
			role = "admin"
		}
		pw := server.GeneratePassword()
		hash, err := server.HashPassword(pw)
		if err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO users (login, name, role, password_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
			args[1], args[2], role, hash, time.Now().UnixMilli()); err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", args[1], pw)
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func fatal(err error) {
	slog.Error(err.Error())
	os.Exit(1)
}
