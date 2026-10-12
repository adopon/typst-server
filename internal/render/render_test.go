package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	dir := t.TempDir()

	ok := []string{"/data.json", "data.json", "/a/b.txt", "./x.txt", "/.fonts/Arimo/a.ttf"}
	for _, p := range ok {
		if _, err := resolve(dir, p); err != nil {
			t.Errorf("resolve(%q) = error %v, want nil", p, err)
		}
	}

	bad := []string{"../evil", "/../evil", "a/../../evil", "../../etc/passwd"}
	for _, p := range bad {
		if _, err := resolve(dir, p); err == nil {
			t.Errorf("resolve(%q) = nil error, want escape rejection", p)
		}
	}
}

// writeStub installs a fake typst binary. `script` runs with the shadow dir as
// cwd, so `echo -n pdf > out.pdf` mimics a successful compile.
func writeStub(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "typst-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestRenderer(t *testing.T, script string) *Renderer {
	t.Helper()
	r, err := New(writeStub(t, script), 0)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewRejectsMissingBinary(t *testing.T) {
	if _, err := New("typst-server-definitely-missing", 0); err == nil {
		t.Fatal("expected an error for a missing typst binary")
	}
}

func TestRenderWritesPdf(t *testing.T) {
	r := newTestRenderer(t, `echo -n "PDF-BYTES" > out.pdf`)

	pdf, err := r.Render(Request{Template: "= hi"})
	if err != nil {
		t.Fatal(err)
	}
	if string(pdf) != "PDF-BYTES" {
		t.Fatalf("Render() = %q, want %q", pdf, "PDF-BYTES")
	}
}

func TestRenderPassesFontPathsAndInputs(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("ARGS_FILE", argsFile)
	r := newTestRenderer(t, `printf '%s\n' "$@" > "$ARGS_FILE"; echo -n pdf > out.pdf`)

	_, err := r.Render(Request{
		Template:  "= hi",
		Files:     map[string][]byte{"/fonts/test.ttf": []byte("fake-font"), "/data.json": []byte("{}")},
		FontPaths: []string{"/fonts"},
		Inputs:    map[string]string{"version": "1.2.0"},
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := string(raw)
	for _, want := range []string{"--font-path", "--input", "version=1.2.0", "compile", "out.pdf"} {
		if !strings.Contains(args, want) {
			t.Errorf("typst args missing %q in:\n%s", want, args)
		}
	}
}

func TestRenderTimeout(t *testing.T) {
	r := newTestRenderer(t, `exec sleep 5`)

	ms := int64(1000)
	start := time.Now()
	_, err := r.Render(Request{Template: "= hi", TimeoutMs: &ms})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Render() error = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %s, want ~1s", elapsed)
	}
}

func TestTimeoutFor(t *testing.T) {
	cases := []struct {
		name string
		ms   *int64
		want time.Duration
	}{
		{"default", nil, DefaultTimeout},
		{"clamped low", ptr(int64(10)), DefaultTimeout},
		{"clamped high", ptr(int64(999_999)), DefaultTimeout},
		{"explicit", ptr(int64(5000)), 5 * time.Second},
	}
	for _, tc := range cases {
		if got := TimeoutFor(Request{TimeoutMs: tc.ms}); got != tc.want {
			t.Errorf("%s: TimeoutFor() = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }
