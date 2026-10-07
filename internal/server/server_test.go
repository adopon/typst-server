package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ponado-typst/internal/render"
)

type fakeRenderer struct {
	pdf   []byte
	err   error
	calls int
	last  render.Request
}

func (f *fakeRenderer) Render(req render.Request) ([]byte, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return f.pdf, nil
}

func TestHandleRenderJSON(t *testing.T) {
	fake := &fakeRenderer{pdf: []byte("PDF-BYTES")}
	s := New(fake, Options{TypstVersion: "typst test", MaxBodyBytes: 1 << 20})

	b64 := base64.StdEncoding.EncodeToString([]byte(`{"n":1}`))
	req := jsonRequest(`{"template":"= hi","files":{"/data.json":"` + b64 + `"}}`)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("content-type"); got != "application/pdf" {
		t.Fatalf("content-type = %q", got)
	}
	if rec.Body.String() != "PDF-BYTES" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if string(fake.last.Files["/data.json"]) != `{"n":1}` {
		t.Fatalf("renderer got files[/data.json] = %q", fake.last.Files["/data.json"])
	}
}

func TestHandleRenderMultipart(t *testing.T) {
	fake := &fakeRenderer{pdf: []byte("PDF-BYTES")}
	s := New(fake, Options{TypstVersion: "typst test", MaxBodyBytes: 1 << 20})

	req := multipartRequest(t, func(mw *multipart.Writer) {
		_ = mw.WriteField("template", "= hi")
		w, _ := mw.CreateFormFile("files", "data.json")
		_, _ = w.Write([]byte(`{"n":1}`))
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "PDF-BYTES" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if fake.last.Template != "= hi" || string(fake.last.Files["/data.json"]) != `{"n":1}` {
		t.Fatalf("renderer got %+v", fake.last)
	}
}

func TestHandleRenderRequiresTemplate(t *testing.T) {
	s := New(&fakeRenderer{}, Options{MaxBodyBytes: 1 << 20})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonRequest(`{"files":{}}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRenderErrorStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"compile error", errors.New("typst compile failed: boom"), http.StatusUnprocessableEntity},
		{"timeout", errors.New("typst compile timed out after 1000ms"), http.StatusRequestTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(&fakeRenderer{err: tc.err}, Options{MaxBodyBytes: 1 << 20})
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, jsonRequest(`{"template":"= hi"}`))

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("error body is not JSON: %v", err)
			}
			if !strings.Contains(body["error"], tc.err.Error()) {
				t.Fatalf("error = %q, want it to contain %q", body["error"], tc.err.Error())
			}
		})
	}
}

func TestHandleRenderRejectsOversizeBody(t *testing.T) {
	s := New(&fakeRenderer{}, Options{MaxBodyBytes: 16}) // 16-byte cap
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, jsonRequest(`{"template":"longer than sixteen bytes"}`))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestHealthAndVersion(t *testing.T) {
	s := New(&fakeRenderer{}, Options{
		ServiceVersion: "1.2.3",
		TypstVersion:   "typst 0.15.1",
		MaxBodyBytes:   1 << 20,
	})

	health := httptest.NewRecorder()
	s.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK || health.Body.String() != "ok\n" {
		t.Fatalf("health = %d %q", health.Code, health.Body.String())
	}

	version := httptest.NewRecorder()
	s.Handler().ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/version", nil))
	var body map[string]string
	if err := json.Unmarshal(version.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["service"] != Name || body["typst"] != "typst 0.15.1" || body["version"] != "1.2.3" {
		t.Fatalf("version = %v", body)
	}
}
