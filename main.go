// Command ponado-typst serves the shared Typst -> PDF renderer
// ("Gotenberg, but only for Typst"). The wire contract and deployment notes
// live in README.md; the implementation is split into:
//
//	internal/config  environment parsing
//	internal/render  typst invocation, shadow dirs, timeouts, concurrency
//	internal/server  HTTP handlers + JSON/multipart request decoding
package main

import (
	"log"
	"net/http"

	"ponado-typst/internal/config"
	"ponado-typst/internal/render"
	"ponado-typst/internal/server"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatal(err)
	}

	renderer, err := render.New(cfg.TypstBin, cfg.MaxConcurrency)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.MaxConcurrency > 0 {
		log.Printf("max concurrent renders: %d", cfg.MaxConcurrency)
	} else {
		log.Printf("max concurrent renders: unlimited")
	}

	typstVersion := renderer.Version()
	if typstVersion == "" {
		log.Printf("warning: could not determine typst version")
	}

	srv := server.New(renderer, typstVersion, cfg.MaxBodyBytes)
	addr := "0.0.0.0:" + cfg.Port
	log.Printf("%s listening on %s (typst: %s)", server.Name, addr, typstVersion)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
