// Package render compiles Typst templates to PDF by shelling out to the
// prebuilt typst CLI. It owns per-request shadow dirs, client-path clamping,
// timeouts and the compile-concurrency cap. No HTTP awareness lives here.
package render

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Defaults and bounds for one compile.
const (
	DefaultMainPath = "/main.typ"
	DefaultTimeout  = 30 * time.Second
	MinTimeout      = 1 * time.Second
	MaxTimeout      = 60 * time.Second
)

// Request describes one compile. Files and FontPaths use shadow paths
// ("/data.json", "/.fonts"); they are clamped to the request's temp dir.
//
// Files are always flat: services pass named files, not directory trees.
type Request struct {
	Template     string
	MainFilePath string
	Files        map[string][]byte
	FontPaths    []string
	Inputs       map[string]string
	TimeoutMs    *int64
}

// Renderer compiles requests. It is safe for concurrent use; create one per
// process.
type Renderer struct {
	bin string
	sem chan struct{} // nil means unlimited
}

// New resolves the typst binary and creates a renderer. maxConcurrency <= 0
// means unlimited concurrent compiles.
func New(bin string, maxConcurrency int) (*Renderer, error) {
	resolved, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("typst binary %q not found: %w", bin, err)
	}
	r := &Renderer{bin: resolved}
	if maxConcurrency > 0 {
		r.sem = make(chan struct{}, maxConcurrency)
	}
	return r, nil
}

// Version returns the `typst --version` banner, or "" when unavailable.
func (r *Renderer) Version() string {
	out, err := exec.Command(r.bin, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TimeoutFor resolves a request's timeout, clamping to [MinTimeout, MaxTimeout].
func TimeoutFor(req Request) time.Duration {
	t := DefaultTimeout
	if req.TimeoutMs != nil {
		if ms := *req.TimeoutMs; ms >= MinTimeout.Milliseconds() && ms <= MaxTimeout.Milliseconds() {
			t = time.Duration(ms) * time.Millisecond
		}
	}
	return t
}

// resolve maps a client path ("/data.json" or "../evil") onto a safe path
// inside dir. Guarantees the result stays within dir.
func resolve(dir, path string) (string, error) {
	rel := filepath.Clean(strings.TrimPrefix(path, "/"))
	abs := filepath.Join(dir, rel)
	if abs != dir && !strings.HasPrefix(abs, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes temp dir: %s", path)
	}
	return abs, nil
}

// Render compiles the request and returns the PDF bytes.
func (r *Renderer) Render(req Request) ([]byte, error) {
	if req.MainFilePath == "" {
		req.MainFilePath = DefaultMainPath
	}

	timeout := TimeoutFor(req)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Each request forks a typst process; cap fan-in so a shared service
	// cannot be turned into a fork bomb.
	if err := r.acquire(ctx); err != nil {
		return nil, fmt.Errorf("render timed out after %dms waiting for a free slot", timeout.Milliseconds())
	}
	defer r.release()

	dir, err := os.MkdirTemp("", "ponado-typst-")
	if err != nil {
		return nil, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := writeShadowFiles(dir, req); err != nil {
		return nil, err
	}

	flags, err := renderFlags(dir, req)
	if err != nil {
		return nil, err
	}
	args := append([]string{"compile", strings.TrimPrefix(req.MainFilePath, "/"), "out.pdf"}, flags...)

	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("typst compile timed out after %dms", timeout.Milliseconds())
		}
		return nil, fmt.Errorf("typst compile failed: %s", strings.TrimSpace(string(out)))
	}

	return os.ReadFile(filepath.Join(dir, "out.pdf"))
}

// writeShadowFiles materializes the template and the request's files into dir.
func writeShadowFiles(dir string, req Request) error {
	main, err := resolve(dir, req.MainFilePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(main), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(main, []byte(req.Template), 0o644); err != nil {
		return fmt.Errorf("write main: %w", err)
	}

	for path, data := range req.Files {
		abs, err := resolve(dir, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// renderFlags builds the optional typst CLI flags for a request.
func renderFlags(dir string, req Request) ([]string, error) {
	var flags []string

	if len(req.FontPaths) > 0 {
		paths := make([]string, 0, len(req.FontPaths))
		for _, p := range req.FontPaths {
			abs, err := resolve(dir, p)
			if err != nil {
				return nil, err
			}
			paths = append(paths, abs)
		}
		flags = append(flags, "--font-path", strings.Join(paths, string(os.PathListSeparator)))
	}

	for k, v := range req.Inputs {
		flags = append(flags, "--input", k+"="+v)
	}
	return flags, nil
}

func (r *Renderer) acquire(ctx context.Context) error {
	if r.sem == nil {
		return nil
	}
	select {
	case r.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Renderer) release() {
	if r.sem != nil {
		<-r.sem
	}
}
