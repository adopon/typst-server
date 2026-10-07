package server

import (
	"bytes"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func jsonRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/render", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	return req
}

func multipartRequest(t *testing.T, build func(*multipart.Writer)) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	build(mw)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/render", &buf)
	req.Header.Set("content-type", mw.FormDataContentType())
	return req
}

func TestDecodeJSONReq(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("{}"))
	s := New(&fakeRenderer{}, Options{MaxBodyBytes: 1 << 20})

	req, err := s.decodeRequest(httptest.NewRecorder(), jsonRequest(
		`{"template":"= hi","files":{"/data.json":"`+b64+`"},"inputs":{"a":"b"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if string(req.Files["/data.json"]) != "{}" {
		t.Errorf("files[/data.json] = %q, want {}", req.Files["/data.json"])
	}
	if req.Inputs["a"] != "b" {
		t.Errorf("inputs[a] = %q, want b", req.Inputs["a"])
	}
}

func TestDecodeJSONReqRejectsBadBase64(t *testing.T) {
	s := New(&fakeRenderer{}, Options{MaxBodyBytes: 1 << 20})
	_, err := s.decodeRequest(httptest.NewRecorder(), jsonRequest(
		`{"template":"= hi","files":{"/data.json":"!!!not-base64"}}`,
	))
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("decodeRequest() error = %v, want base64 rejection", err)
	}
}

func TestDecodeMultipartReq(t *testing.T) {
	s := New(&fakeRenderer{}, Options{MaxBodyBytes: 1 << 20})

	req := multipartRequest(t, func(mw *multipart.Writer) {
		_ = mw.WriteField("template", "= hi")
		_ = mw.WriteField("mainFilePath", "/main.typ")
		_ = mw.WriteField("timeoutMs", "5000")
		_ = mw.WriteField("inputs", `{"version":"1.2.0"}`)
		_ = mw.WriteField("fontPaths", `["/fonts"]`)

		w, _ := mw.CreateFormFile("files", "data.json")
		_, _ = w.Write([]byte(`{"n":1}`))
		w, _ = mw.CreateFormFile("files", "logo.png")
		_, _ = w.Write([]byte("PNG"))
		// filename with traversal segments must flatten to the base name
		w, _ = mw.CreateFormFile("files", "../../evil.txt")
		_, _ = w.Write([]byte("x"))
	})

	decoded, err := s.decodeRequest(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Template != "= hi" {
		t.Errorf("template = %q", decoded.Template)
	}
	if decoded.MainFilePath != "/main.typ" {
		t.Errorf("mainFilePath = %q", decoded.MainFilePath)
	}
	if decoded.TimeoutMs == nil || *decoded.TimeoutMs != 5000 {
		t.Errorf("timeoutMs = %v, want 5000", decoded.TimeoutMs)
	}
	if decoded.Inputs["version"] != "1.2.0" {
		t.Errorf("inputs[version] = %q", decoded.Inputs["version"])
	}
	if len(decoded.FontPaths) != 1 || decoded.FontPaths[0] != "/fonts" {
		t.Errorf("fontPaths = %v", decoded.FontPaths)
	}
	if string(decoded.Files["/data.json"]) != `{"n":1}` {
		t.Errorf("files[/data.json] = %q", decoded.Files["/data.json"])
	}
	if string(decoded.Files["/logo.png"]) != "PNG" {
		t.Errorf("files[/logo.png] = %q", decoded.Files["/logo.png"])
	}
	if string(decoded.Files["/evil.txt"]) != "x" {
		t.Errorf("files[/evil.txt] = %q, want traversal flattened", decoded.Files["/evil.txt"])
	}
	if _, escaped := decoded.Files["/../../evil.txt"]; escaped {
		t.Error("nested traversal path survived decoding")
	}
}

func TestDecodeMultipartRejectsBadInputs(t *testing.T) {
	s := New(&fakeRenderer{}, Options{MaxBodyBytes: 1 << 20})

	req := multipartRequest(t, func(mw *multipart.Writer) {
		_ = mw.WriteField("template", "= hi")
		_ = mw.WriteField("inputs", "not-json")
	})
	if _, err := s.decodeRequest(httptest.NewRecorder(), req); err == nil {
		t.Fatal("invalid inputs JSON accepted")
	}
}
