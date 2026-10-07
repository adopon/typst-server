// Package server exposes the HTTP surface of ponado-typst:
//
//	GET  /health  -> "ok"
//	GET  /version -> {"service":"ponado-typst","go":"go1.x","typst":"typst 0.15.1"}
//	POST /render  -> application/pdf | application/json error
//
// POST /render accepts both application/json (files base64-encoded) and
// multipart/form-data (files as binary parts). See decode.go for the mapping.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"ponado-typst/internal/render"
)

// Name is the service name reported by /version and used in logs.
const Name = "ponado-typst"

// Renderer is the compile backend; internal/render satisfies it.
type Renderer interface {
	Render(render.Request) ([]byte, error)
}

// Server serves the wire contract for one renderer and configuration.
type Server struct {
	renderer     Renderer
	typstVersion string
	maxBodyBytes int64
}

// New wires a server. typstVersion is informational (/version, logs only).
func New(renderer Renderer, typstVersion string, maxBodyBytes int64) *Server {
	return &Server{
		renderer:     renderer,
		typstVersion: typstVersion,
		maxBodyBytes: maxBodyBytes,
	}
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("POST /render", s.handleRender)
	return mux
}

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	req, err := s.decodeRequest(w, r)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			apiError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds %dMB", s.maxBodyBytes>>20))
			return
		}
		apiError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.Template == "" {
		apiError(w, http.StatusBadRequest, "template is required")
		return
	}

	pdf, err := s.renderer.Render(req)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if strings.Contains(err.Error(), "timed out") {
			status = http.StatusRequestTimeout
		}
		log.Printf("render failed in %s: %v", time.Since(start).Round(time.Millisecond), err)
		apiError(w, status, err.Error())
		return
	}

	log.Printf("render ok in %s (%d shadow files, %d inputs, %d bytes pdf)",
		time.Since(start).Round(time.Millisecond), len(req.Files), len(req.Inputs), len(pdf))

	w.Header().Set("content-type", "application/pdf")
	_, _ = w.Write(pdf)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"service": Name,
		"go":      runtime.Version(),
		"typst":   s.typstVersion,
	})
}

func apiError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
