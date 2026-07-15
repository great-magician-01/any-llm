package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/great-magician-01/any-llm/internal/config"
	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/gateway"
	"github.com/great-magician-01/any-llm/internal/upstream"
	"github.com/great-magician-01/any-llm/internal/webapi"
)

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

	client := upstream.NewClient(nil)
	gw := gateway.New(d, client)
	api := webapi.NewAPI(d, client)

	mux := http.NewServeMux()
	mux.Handle("/v1/", gw)
	mux.Handle("/api/admin/", api.Handler())

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("any-llm listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
