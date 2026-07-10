package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/nasweb/nasd/internal/filemgr"
	"github.com/nasweb/nasd/internal/ws"
)

func setupFilesRouter(t *testing.T) (*chi.Mux, string) {
	t.Helper()
	root := t.TempDir()
	h := &fileHandlers{mgr: filemgr.NewManager(root), hub: ws.NewHub()}
	r := chi.NewRouter()
	r.Get("/files", h.list)
	r.Get("/files/download", h.download)
	r.Post("/files/mkdir", h.mkdir)
	r.Post("/files/rename", h.rename)
	r.Post("/files/chmod", h.chmod)
	r.Post("/files/upload", h.upload)
	r.Delete("/files", h.remove)
	return r, root
}

func TestFilesMkdirAndList(t *testing.T) {
	r, _ := setupFilesRouter(t)

	if rec := do(t, r, http.MethodPost, "/files/mkdir", `{"path":"docs"}`); rec.Code != http.StatusCreated {
		t.Fatalf("mkdir: %d %s", rec.Code, rec.Body.String())
	}
	rec := do(t, r, http.MethodGet, "/files?path=/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var entries []filemgr.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "docs" || !entries[0].IsDir {
		t.Fatalf("atteso 1 dir 'docs', ottenuto %+v", entries)
	}
}

func TestFilesUploadAndDownload(t *testing.T) {
	r, _ := setupFilesRouter(t)

	// Costruisce un body multipart con un file.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", "hello.txt")
	fw.Write([]byte("ciao NAS"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/files/upload?path=/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}

	// Download del file appena caricato.
	rec = do(t, r, http.MethodGet, "/files/download?path=/hello.txt", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("download: %d", rec.Code)
	}
	if rec.Body.String() != "ciao NAS" {
		t.Fatalf("contenuto = %q", rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="hello.txt"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
}

func TestFilesDeleteRequiresConfirm(t *testing.T) {
	r, _ := setupFilesRouter(t)
	do(t, r, http.MethodPost, "/files/mkdir", `{"path":"tmp"}`)

	if rec := do(t, r, http.MethodDelete, "/files?path=/tmp", ""); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("delete senza confirm: atteso 428, ottenuto %d", rec.Code)
	}
	if rec := do(t, r, http.MethodDelete, "/files?path=/tmp&confirm=true", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete con confirm: atteso 200, ottenuto %d", rec.Code)
	}
}

func TestFilesChmodValidation(t *testing.T) {
	r, _ := setupFilesRouter(t)
	// crea un file via upload
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", "f.bin")
	fw.Write([]byte("x"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/files/upload?path=/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	httptestServe(r, req)

	if rec := do(t, r, http.MethodPost, "/files/chmod", `{"path":"/f.bin","mode":"640"}`); rec.Code != http.StatusOK {
		t.Fatalf("chmod valido: %d %s", rec.Code, rec.Body.String())
	}
	// mode fuori range -> 422
	if rec := do(t, r, http.MethodPost, "/files/chmod", `{"path":"/f.bin","mode":"7777"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("chmod fuori range: atteso 422, ottenuto %d", rec.Code)
	}
	// mode non ottale -> 422
	if rec := do(t, r, http.MethodPost, "/files/chmod", `{"path":"/f.bin","mode":"abc"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("chmod non ottale: atteso 422, ottenuto %d", rec.Code)
	}
}

func TestFilesPathTraversalRejected(t *testing.T) {
	r, _ := setupFilesRouter(t)
	// Tentativo di uscire dalla root: deve fallire (jail), non listare /.
	rec := do(t, r, http.MethodGet, "/files?path=../../../../etc", "")
	if rec.Code == http.StatusOK {
		t.Fatalf("path traversal non bloccato: status %d body %s", rec.Code, rec.Body.String())
	}
}

func httptestServe(r http.Handler, req *http.Request) {
	r.ServeHTTP(httptest.NewRecorder(), req)
}
