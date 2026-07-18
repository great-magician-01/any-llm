package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/great-magician-01/any-llm/internal/auth"
	"github.com/great-magician-01/any-llm/internal/config"
	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/gateway"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/upstream"
	"github.com/great-magician-01/any-llm/internal/usage"
	"github.com/great-magician-01/any-llm/internal/webapi"
)

//go:embed web/dist
var frontend embed.FS

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load config:", err)
		os.Exit(1)
	}

	if err := logger.Init(logger.Options{Level: cfg.LogLevel, FilePath: cfg.LogFile}); err != nil {
		fmt.Fprintln(os.Stderr, "init logger:", err)
		os.Exit(1)
	}
	defer logger.Close()
	logger.Info("any-llm starting", "host", cfg.Host, "port", cfg.Port, "db", cfg.DBPath, "log_file", cfg.LogFile, "log_level", cfg.LogLevel.String())

	d, err := db.Open(cfg.DBPath)
	if err != nil {
		logger.Error("open db failed", "err", err, "path", cfg.DBPath)
		os.Exit(1)
	}
	defer d.Close()

	rec := usage.NewRecorder(d, 256)
	rec.Start()
	defer rec.Stop()

	client := upstream.NewClient(nil)
	gw := gateway.New(d, client, rec)
	gw.Start()
	defer gw.Stop()

	api := webapi.NewAPI(d, client)
	authM := auth.NewMiddleware(cfg.SessionSecret, cfg.MasterPassword)
	adminHandler := authM.Wrap(api.Handler())

	frontendFS, err := fs.Sub(frontend, "web/dist")
	if err != nil {
		logger.Error("frontend fs failed", "err", err)
		os.Exit(1)
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
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if f, err := frontendFS.Open(path); err != nil {
				r.URL.Path = "/"
			} else {
				f.Close()
			}
		}
		spa.ServeHTTP(w, r)
	})

	logger.Infof("any-llm listening on %s:%d", cfg.Host, cfg.Port)
	if err := http.ListenAndServe(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), mux); err != nil {
		logger.Errorf("server failed: %v", err)
		os.Exit(1)
	}
}
