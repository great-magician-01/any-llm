package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/auth"
	"github.com/great-magician-01/any-llm/internal/config"
	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/gateway"
	"github.com/great-magician-01/any-llm/internal/upstream"
	"github.com/great-magician-01/any-llm/internal/usage"
	"github.com/great-magician-01/any-llm/internal/webapi"
)

//go:embed web/dist
var frontend embed.FS

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	d, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
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
		log.Fatalf("frontend fs: %v", err)
	}
	spa := http.FileServer(http.FS(frontendFS))

	mux := http.NewServeMux()
	mux.Handle("/v1/", gw)
	mux.Handle("/api/admin/", adminHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}
		r.URL.Path = "/"
		spa.ServeHTTP(w, r)
	})

	log.Printf("any-llm listening on :%d", cfg.Port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
