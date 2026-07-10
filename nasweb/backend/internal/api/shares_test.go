package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/nasweb/nasd/internal/shares"
	"github.com/nasweb/nasd/internal/ws"
)

// fakeRunner soddisfa system.Runner senza eseguire comandi reali: i generatori
// di config scrivono i file (su tmp) e i reload (exportfs/testparm/...) sono no-op.
type fakeRunner struct{}

func (fakeRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	return "", "", nil
}
func (fakeRunner) RunStdin(ctx context.Context, stdin, name string, args ...string) (string, string, error) {
	return "", "", nil
}

func setupSharesRouter(t *testing.T) (*chi.Mux, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1) // mantiene viva l'unica connessione in-memory
	if _, err := db.Exec(`CREATE TABLE shares (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL, path TEXT NOT NULL,
		protocol TEXT NOT NULL CHECK (protocol IN ('nfs','smb')),
		read_only INTEGER NOT NULL DEFAULT 0,
		allowed_ips TEXT DEFAULT '', valid_users TEXT DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		UNIQUE(name, protocol))`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	tmp := t.TempDir()
	mgr := shares.NewManager(fakeRunner{}, filepath.Join(tmp, "exports"), filepath.Join(tmp, "smb.conf"))
	h := &sharesHandlers{db: db, mgr: mgr, hub: ws.NewHub()}

	r := chi.NewRouter()
	r.Get("/shares", h.list)
	r.Post("/shares", h.create)
	r.Put("/shares/{id}", h.update)
	r.Delete("/shares/{id}", h.delete)
	t.Cleanup(func() { db.Close() })
	return r, db
}

func do(t *testing.T, r http.Handler, method, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, url, rd)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSharesCreateAndList(t *testing.T) {
	r, _ := setupSharesRouter(t)

	rec := do(t, r, http.MethodPost, "/shares",
		`{"name":"media","path":"/srv/media","protocol":"nfs","read_only":true,"allowed_ips":["192.168.1.0/24"],"enabled":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = do(t, r, http.MethodGet, "/shares", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d", rec.Code)
	}
	var got []shareDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("atteso 1 share, ottenute %d", len(got))
	}
	s := got[0]
	if s.ID == 0 || s.Name != "media" || s.Protocol != shares.NFS || !s.ReadOnly {
		t.Fatalf("share inattesa: %+v", s)
	}
	if len(s.AllowedIPs) != 1 || s.AllowedIPs[0] != "192.168.1.0/24" {
		t.Fatalf("allowed_ips non round-trippato: %+v", s.AllowedIPs)
	}
}

func TestSharesCreateRejectsBadInput(t *testing.T) {
	r, _ := setupSharesRouter(t)
	cases := []string{
		`{"name":"bad name!","path":"/srv/x","protocol":"nfs","enabled":true}`, // nome con '!'
		`{"name":"ok","path":"relativo","protocol":"nfs","enabled":true}`,      // path non assoluto
		`{"name":"ok","path":"/srv/x","protocol":"ftp","enabled":true}`,        // protocollo ignoto
	}
	for _, body := range cases {
		rec := do(t, r, http.MethodPost, "/shares", body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("input %q: atteso 422, ottenuto %d", body, rec.Code)
		}
	}
}

func TestSharesDisableRequiresConfirm(t *testing.T) {
	r, _ := setupSharesRouter(t)
	if rec := do(t, r, http.MethodPost, "/shares",
		`{"name":"docs","path":"/srv/docs","protocol":"smb","enabled":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("setup create: %d %s", rec.Code, rec.Body.String())
	}

	// Disabilitare senza confirm -> 428.
	rec := do(t, r, http.MethodPut, "/shares/1",
		`{"name":"docs","path":"/srv/docs","protocol":"smb","enabled":false}`)
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("disable senza confirm: atteso 428, ottenuto %d", rec.Code)
	}

	// Con confirm -> 200.
	rec = do(t, r, http.MethodPut, "/shares/1",
		`{"name":"docs","path":"/srv/docs","protocol":"smb","enabled":false,"confirm":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable con confirm: atteso 200, ottenuto %d %s", rec.Code, rec.Body.String())
	}
}

func TestSharesDeleteRequiresConfirm(t *testing.T) {
	r, _ := setupSharesRouter(t)
	if rec := do(t, r, http.MethodPost, "/shares",
		`{"name":"temp","path":"/srv/temp","protocol":"nfs","enabled":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("setup create: %d", rec.Code)
	}

	if rec := do(t, r, http.MethodDelete, "/shares/1", ""); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("delete senza confirm: atteso 428, ottenuto %d", rec.Code)
	}
	if rec := do(t, r, http.MethodDelete, "/shares/1?confirm=true", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete con confirm: atteso 200, ottenuto %d", rec.Code)
	}
	// Una seconda delete non trova nulla -> 404.
	if rec := do(t, r, http.MethodDelete, "/shares/1?confirm=true", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete inesistente: atteso 404, ottenuto %d", rec.Code)
	}
}
