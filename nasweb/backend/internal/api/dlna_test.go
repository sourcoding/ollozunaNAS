package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/nasweb/nasd/internal/dlna"
	"github.com/nasweb/nasd/internal/ws"
)

func setupDlnaRouter(t *testing.T) (*chi.Mux, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE media_dirs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL UNIQUE, media_type TEXT DEFAULT '');
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO settings(key,value) VALUES('nas_name','TestNAS');`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	tmp := t.TempDir()
	mgr := dlna.NewManager(fakeRunner{}, filepath.Join(tmp, "minidlna.conf"))
	h := &dlnaHandlers{db: db, mgr: mgr, hub: ws.NewHub()}
	r := chi.NewRouter()
	r.Get("/dlna/dirs", h.listDirs)
	r.Post("/dlna/dirs", h.addDir)
	r.Delete("/dlna/dirs", h.removeDir)
	r.Post("/dlna/rescan", h.rescan)
	t.Cleanup(func() { db.Close() })
	return r, db
}

func TestDlnaAddAndList(t *testing.T) {
	r, _ := setupDlnaRouter(t)
	if rec := do(t, r, http.MethodPost, "/dlna/dirs", `{"path":"/srv/media/video","type":"V"}`); rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	rec := do(t, r, http.MethodGet, "/dlna/dirs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var dirs []dlna.MediaDir
	if err := json.Unmarshal(rec.Body.Bytes(), &dirs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dirs) != 1 || dirs[0].Path != "/srv/media/video" || dirs[0].Type != "V" {
		t.Fatalf("inatteso: %+v", dirs)
	}
}

func TestDlnaAddRejectsBadInput(t *testing.T) {
	r, _ := setupDlnaRouter(t)
	cases := []string{
		`{"path":"relativo","type":"V"}`,       // non assoluto
		`{"path":"/srv/x","type":"Z"}`,         // tipo non valido
		`{"path":"/srv/x\ninject","type":"V"}`, // newline nel path
	}
	for _, body := range cases {
		if rec := do(t, r, http.MethodPost, "/dlna/dirs", body); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("input %q: atteso 422, ottenuto %d", body, rec.Code)
		}
	}
}

func TestDlnaRemoveAndRescan(t *testing.T) {
	r, _ := setupDlnaRouter(t)
	do(t, r, http.MethodPost, "/dlna/dirs", `{"path":"/srv/media/music","type":"A"}`)

	if rec := do(t, r, http.MethodDelete, "/dlna/dirs?path=/srv/media/music", ""); rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, r, http.MethodDelete, "/dlna/dirs?path=/srv/media/music", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("remove inesistente: atteso 404, ottenuto %d", rec.Code)
	}
	if rec := do(t, r, http.MethodPost, "/dlna/rescan", ""); rec.Code != http.StatusOK {
		t.Fatalf("rescan: %d", rec.Code)
	}
}
