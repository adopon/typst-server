package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"typst-server/internal/render"
)

// multipartMemory caps the RAM used while parsing multipart bodies; larger
// parts spill to temp files. The request body itself is already capped.
const multipartMemory = 8 << 20

// payload is the request wire shape. JSON carries files base64-encoded;
// multipart carries them as binary parts. See fileMap.
type payload struct {
	Template     string            `json:"template"`
	MainFilePath string            `json:"mainFilePath"`
	Files        fileMap           `json:"files"`
	FontPaths    []string          `json:"fontPaths"`
	Inputs       map[string]string `json:"inputs"`
	TimeoutMs    *int64            `json:"timeoutMs"`
}

func (p payload) toRequest() render.Request {
	return render.Request{
		Template:     p.Template,
		MainFilePath: p.MainFilePath,
		Files:        map[string][]byte(p.Files),
		FontPaths:    p.FontPaths,
		Inputs:       p.Inputs,
		TimeoutMs:    p.TimeoutMs,
	}
}

// fileMap is `{ path: base64 }` on the JSON wire; raw bytes internally.
type fileMap map[string][]byte

func (m *fileMap) UnmarshalJSON(data []byte) error {
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(fileMap, len(raw))
	for path, b64 := range raw {
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return fmt.Errorf("file %s is not valid base64", path)
		}
		out[path] = decoded
	}
	*m = out
	return nil
}

// decodeRequest dispatches on Content-Type. Both encodings map onto the same
// render.Request; clients pick the one that fits their payload.
func (s *Server) decodeRequest(w http.ResponseWriter, r *http.Request) (render.Request, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("content-type"))
	if err == nil && mediaType == "multipart/form-data" {
		return decodeMultipart(r)
	}

	var p payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return render.Request{}, err
	}
	return p.toRequest(), nil
}

// decodeMultipart maps multipart/form-data onto a render.Request:
//
//	text fields: template, mainFilePath, timeoutMs,
//	             inputs    = JSON object string (optional),
//	             fontPaths = JSON array string (optional)
//	file parts:  land flat in the shadow root by filename, e.g.
//	             `curl -F 'files=@data.json'` -> /data.json
func decodeMultipart(r *http.Request) (render.Request, error) {
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		return render.Request{}, err
	}

	var p payload
	p.Template = r.FormValue("template")
	p.MainFilePath = r.FormValue("mainFilePath")

	if raw := strings.TrimSpace(r.FormValue("timeoutMs")); raw != "" {
		ms, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return render.Request{}, fmt.Errorf("timeoutMs is not a number: %q", raw)
		}
		p.TimeoutMs = &ms
	}
	if raw := strings.TrimSpace(r.FormValue("inputs")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &p.Inputs); err != nil {
			return render.Request{}, fmt.Errorf("inputs is not a JSON object: %w", err)
		}
	}
	if raw := strings.TrimSpace(r.FormValue("fontPaths")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &p.FontPaths); err != nil {
			return render.Request{}, fmt.Errorf("fontPaths is not a JSON array: %w", err)
		}
	}

	p.Files = fileMap{}
	for field, headers := range r.MultipartForm.File {
		for _, fh := range headers {
			// FileName() is already filepath.Base()-ed by net/http.
			name := filepath.Base(strings.TrimSpace(fh.Filename))
			if name == "" || name == "." || name == ".." {
				return render.Request{}, fmt.Errorf("file part %q has an invalid filename %q", field, fh.Filename)
			}
			path := "/" + name
			f, err := fh.Open()
			if err != nil {
				return render.Request{}, fmt.Errorf("read %s: %w", path, err)
			}
			data, err := io.ReadAll(f)
			_ = f.Close()
			if err != nil {
				return render.Request{}, fmt.Errorf("read %s: %w", path, err)
			}
			p.Files[path] = data
		}
	}
	if len(p.Files) == 0 {
		p.Files = nil
	}
	return p.toRequest(), nil
}
