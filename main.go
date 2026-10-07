// ponado-typst: a small shared HTTP service that compiles Typst templates to
// PDF - "Gotenberg, but only for Typst". Stdlib only (no module deps), shells
// out to the prebuilt `typst` CLI that ships in the container image.
//
// Wire contract (superset of the algis pdf-worker contract - existing clients
// keep working unchanged):
//
//	GET  /health  -> "ok"
//	GET  /version -> {"service":"ponado-typst","go":"go1.x","typst":"typst 0.15.1"}
//	POST /render  -> application/pdf | application/json error
//
//	{
//	  "template":     "= Hello",          // required: Typst source
//	  "mainFilePath": "/main.typ",        // optional: where the template lands
//	  "files":        { "/data.json": "<base64>" }, // optional shadow files
//	  "fontPaths":    ["/.fonts"],        // optional shadow dirs -> --font-path
//	  "inputs":       { "key": "value" }, // optional -> sys.inputs.key
//	  "timeoutMs":    30000               // optional: 1000-60000
//	}
//
// `read("data.json")` inside templates resolves relative to the main file's
// directory. Every request renders in its own temp dir; client paths are
// clamped to that dir (no traversal).
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	serviceName     = "ponado-typst"
	defaultMainPath = "/main.typ"
	defaultTimeout  = 30 * time.Second
	minTimeout      = 1 * time.Second
	maxTimeout      = 60 * time.Second
	defaultBodyMB   = 25
)

type renderReq struct {
	Template     string            `json:"template"`
	MainFilePath string            `json:"mainFilePath"`
	Files        map[string]string `json:"files"`
	FontPaths    []string          `json:"fontPaths"`
	Inputs       map[string]string `json:"inputs"`
	TimeoutMs    *int64            `json:"timeoutMs"`
}

// Global state, set once at startup (or left empty in tests, which then fall
// back to TYPST_BIN/the PATH).
var (
	sem         chan struct{}
	typstPath   string
	typstBanner string
)

func typstBin() string {
	if b := os.Getenv("TYPST_BIN"); b != "" {
		return b
	}
	return "typst"
}

// resolve maps a client path ("/data.json", "/.fonts/x.ttf" or "../evil") onto
// a safe path inside the per-request shadow dir. Guarantees the result stays
// within `dir`.
func resolve(dir, path string) (string, error) {
	rel := filepath.Clean(strings.TrimPrefix(path, "/"))
	abs := filepath.Join(dir, rel)
	if abs != dir && !strings.HasPrefix(abs, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes temp dir: %s", path)
	}
	return abs, nil
}

func timeoutFor(req renderReq) time.Duration {
	t := defaultTimeout
	if req.TimeoutMs != nil {
		if ms := *req.TimeoutMs; ms >= minTimeout.Milliseconds() && ms <= maxTimeout.Milliseconds() {
			t = time.Duration(ms) * time.Millisecond
		}
	}
	return t
}

func maxBodyBytes() int64 {
	mb := defaultBodyMB
	if v := os.Getenv("MAX_BODY_MB"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			mb = parsed
		}
	}
	return int64(mb) << 20
}

// initSemaphore bounds concurrent compilations: each request forks a typst
// process and the service is shared by every app, so unbounded fan-in is a
// fork bomb waiting to happen. MAX_CONCURRENCY <= 0 means unlimited.
func initSemaphore() {
	n := runtime.NumCPU()
	if v := strings.TrimSpace(os.Getenv("MAX_CONCURRENCY")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			n = parsed
		} else {
			log.Fatalf("invalid MAX_CONCURRENCY %q: %v", v, err)
		}
	}
	if n > 0 {
		sem = make(chan struct{}, n)
		log.Printf("max concurrent renders: %d", n)
	} else {
		log.Printf("max concurrent renders: unlimited")
	}
}

func acquireSlot(ctx context.Context) error {
	if sem == nil {
		return nil
	}
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseSlot() {
	if sem != nil {
		<-sem
	}
}

func render(req renderReq) ([]byte, error) {
	if req.MainFilePath == "" {
		req.MainFilePath = defaultMainPath
	}

	timeout := timeoutFor(req)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := acquireSlot(ctx); err != nil {
		return nil, fmt.Errorf("render timed out after %dms waiting for a free slot", timeout.Milliseconds())
	}
	defer releaseSlot()

	dir, err := os.MkdirTemp("", "ponado-typst-")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	main, err := resolve(dir, req.MainFilePath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(main), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(main, []byte(req.Template), 0o644); err != nil {
		return nil, fmt.Errorf("write main: %w", err)
	}

	for path, b64 := range req.Files {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("file %s is not valid base64", path)
		}
		abs, err := resolve(dir, path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir: %w", err)
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
	}

	args := []string{"compile", strings.TrimPrefix(req.MainFilePath, "/"), "out.pdf"}

	if len(req.FontPaths) > 0 {
		paths := make([]string, 0, len(req.FontPaths))
		for _, p := range req.FontPaths {
			abs, err := resolve(dir, p)
			if err != nil {
				return nil, err
			}
			paths = append(paths, abs)
		}
		args = append(args, "--font-path", strings.Join(paths, string(os.PathListSeparator)))
	}

	for k, v := range req.Inputs {
		args = append(args, "--input", k+"="+v)
	}

	cmd := exec.CommandContext(ctx, typstBin(), args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("typst compile timed out after %dms", timeout.Milliseconds())
		}
		return nil, fmt.Errorf("typst compile failed: %s", strings.TrimSpace(string(out)))
	}

	return os.ReadFile(filepath.Join(dir, "out.pdf"))
}

func apiError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func handleRender(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes())
	var req renderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			apiError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body exceeds %dMB", maxBodyBytes()>>20))
			return
		}
		apiError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if req.Template == "" {
		apiError(w, http.StatusBadRequest, "template is required")
		return
	}

	pdf, err := render(req)
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

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"service": serviceName,
		"go":      runtime.Version(),
		"typst":   typstBanner,
	})
}

func main() {
	initSemaphore()

	bin := typstBin()
	path, err := exec.LookPath(bin)
	if err != nil {
		log.Fatalf("typst binary %q not found: %v", bin, err)
	}
	typstPath = path
	if out, err := exec.Command(typstPath, "--version").CombinedOutput(); err == nil {
		typstBanner = strings.TrimSpace(string(out))
	} else {
		log.Printf("warning: could not determine typst version: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "3100"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /version", handleVersion)
	mux.HandleFunc("POST /render", handleRender)

	addr := "0.0.0.0:" + port
	log.Printf("%s listening on %s (typst: %s)", serviceName, addr, typstBanner)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
