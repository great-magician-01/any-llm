package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/great-magician-01/any-llm/internal/auth"
	"github.com/great-magician-01/any-llm/internal/config"
	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/gateway"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/upstream"
	"github.com/great-magician-01/any-llm/internal/webapi"
)

//go:embed web/dist
var frontend embed.FS

// shutdownTimeout bounds how long the server waits for in-flight requests
// (including SSE streams) to finish before connections are force-closed.
const shutdownTimeout = 30 * time.Second

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		return 1
	}

	if err := logger.Init(logger.Options{Level: cfg.LogLevel, FilePath: cfg.LogFile}); err != nil {
		fmt.Fprintln(os.Stderr, "init logger:", err)
		return 1
	}
	defer logger.Close()
	logger.Info("any-llm starting", "host", cfg.Host, "port", cfg.Port, "db_type", cfg.DBType, "log_file", cfg.LogFile, "log_level", cfg.LogLevel.String())

	var d *sql.DB
	switch cfg.DBType {
	case "postgres":
		logger.Info("opening postgres", "host", cfg.DBHost, "port", cfg.DBPort, "db", cfg.DBName, "schema", cfg.DBSchema)
		d, err = db.OpenPG(db.PGConfig{
			Host:     cfg.DBHost,
			Port:     cfg.DBPort,
			User:     cfg.DBUser,
			Password: cfg.DBPassword,
			DBName:   cfg.DBName,
			Schema:   cfg.DBSchema,
		})
	default:
		logger.Info("opening sqlite", "path", cfg.DBPath)
		d, err = db.OpenSQLite(cfg.DBPath)
	}
	if err != nil {
		logger.Error("open db failed", "err", err, "db_type", cfg.DBType)
		return 1
	}
	defer d.Close()

	writer := db.NewWriter(d, 512)
	writer.Start()
	// Deferred calls run LIFO on return: writer.Stop (drains queued writes)
	// → d.Close → logger.Close.
	defer writer.Stop()

	client := upstream.NewClient(nil)
	gw := gateway.New(writer.DB, writer, client)

	api := webapi.NewAPI(writer.DB, writer, client)
	authM := auth.NewMiddleware(cfg.SessionSecret, cfg.MasterPassword)
	adminHandler := authM.Wrap(api.Handler())

	frontendFS, err := fs.Sub(frontend, "web/dist")
	if err != nil {
		logger.Error("frontend fs failed", "err", err)
		return 1
	}
	spa := http.FileServer(http.FS(frontendFS))

	mux := http.NewServeMux()
	mux.Handle("/v1/", gateway.LoggingMiddleware(gw, "gateway"))
	mux.Handle("/api/admin/", gateway.LoggingMiddleware(adminHandler, "admin"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, "/")
		if rel != "" {
			if f, err := frontendFS.Open(rel); err == nil {
				f.Close()
				// Hashed assets under /assets/ are content-addressed and
				// safe to cache aggressively.
				if strings.HasPrefix(rel, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				spa.ServeHTTP(w, r)
				return
			}
			// Missing file with a file extension looks like a stale asset
			// reference (e.g. an old build hash still referenced by a
			// cached index.html). Return 404 instead of index.html so the
			// browser sees a clear error rather than HTML-where-JS-was-
			// expected (which surfaces as a misleading "Failed to fetch
			// dynamically imported module").
			if path.Ext(rel) != "" {
				http.NotFound(w, r)
				return
			}
		}
		// SPA client-side route — always revalidate so the browser picks
		// up fresh chunk hashes on every navigation.
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		r.URL.Path = "/"
		spa.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler: mux,
		// Guards against slowloris; no WriteTimeout because SSE streams are
		// long-lived by design.
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()
	logger.Infof("any-llm listening on %s:%d", cfg.Host, cfg.Port)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return 0
		}
		logger.Errorf("server failed: %v", err)
		return 1
	case <-ctx.Done():
		// A second signal now forces an immediate exit.
		stop()
	}

	logger.Info("shutdown signal received, draining connections", "timeout", shutdownTimeout.String())
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		// Shutdown only fails when the drain deadline passes (e.g. long-lived
		// streams still open); force-close and exit cleanly.
		logger.Warn("graceful drain timed out, force-closing connections", "err", err)
		_ = srv.Close()
	}
	logger.Info("server stopped")
	return 0
}
