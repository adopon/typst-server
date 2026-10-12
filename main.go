// Command typst-server serves the shared Typst -> PDF renderer
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

	"typst-server/internal/config"
	"typst-server/internal/render"
	"typst-server/internal/server"
)

// version is the typst-server release; set via
// -ldflags "-X main.version=..." at image build time.
var version = "dev"

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

	srv := server.New(renderer, server.Options{
		ServiceVersion: version,
		TypstVersion:   typstVersion,
		MaxBodyBytes:   cfg.MaxBodyBytes,
	})
	addr := "0.0.0.0:" + cfg.Port
	log.Printf("%s %s listening on %s (typst: %s)", server.Name, version, addr, typstVersion)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
