// figcraft-admin: a small admin page for figcraft-server at /admin, protected by a password.
//
//	figcraft-admin [serve]       serve the page (the password is made and printed on first start)
//	figcraft-admin reset-password  make a new random password and print it; it works at once
//	                             (run it in the admin container)
//
// Settings come from the environment:
//
//	ADMIN_LISTEN        where to listen (default 0.0.0.0:8090)
//	ADMIN_DATA          where the password hash is kept (default /data/admin)
//	FIGCRAFT_SERVER     the figcraft-server binary (default /usr/local/bin/figcraft-server)
//	FIGCRAFT_CONFIG     its server.toml (default /etc/figcraft-server/server.toml)
//	FIGCRAFT_WEB_URL    the web app's address, for browser links to new shares (optional)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.LUTC)
	dataDir := env("ADMIN_DATA", "/data/admin")
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		if err := serve(logger, dataDir); err != nil {
			logger.Fatalf("figcraft-admin: %v", err)
		}
	case "reset-password":
		p, _, err := OpenPasswords(dataDir)
		if err != nil {
			// A damaged file can't be opened: replace it.
			p = &Passwords{path: dataDir + "/password.json"}
			if mkErr := os.MkdirAll(dataDir, 0o700); mkErr != nil {
				logger.Fatalf("figcraft-admin: %v", err)
			}
		}
		pw, err := p.Reset()
		if err != nil {
			logger.Fatalf("figcraft-admin: %v", err)
		}
		fmt.Printf("New admin password: %s\nIt works at once; sessions already signed in last until the admin container restarts.\n", pw)
	case "-h", "--help", "help":
		fmt.Println("usage: figcraft-admin [serve | reset-password]")
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (serve, reset-password)\n", cmd)
		os.Exit(2)
	}
}

func serve(logger *log.Logger, dataDir string) error {
	passwords, first, err := OpenPasswords(dataDir)
	if err != nil {
		return err
	}
	if first != "" {
		logger.Printf("FIGCRAFT ADMIN PASSWORD (shown once; change it on the admin page): %s", first)
	}
	app := &App{
		Commands: CLI{
			Bin:     env("FIGCRAFT_SERVER", "/usr/local/bin/figcraft-server"),
			Config:  env("FIGCRAFT_CONFIG", "/etc/figcraft-server/server.toml"),
			Timeout: 20 * time.Second,
		},
		Passwords:    passwords,
		Sessions:     NewSessions(),
		Limiter:      NewLimiter(),
		WebURL:       os.Getenv("FIGCRAFT_WEB_URL"),
		SecureCookie: true,
		Log:          logger,
	}
	srv := &http.Server{
		Addr:              env("ADMIN_LISTEN", "0.0.0.0:8090"),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	logger.Printf("figcraft-admin listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
