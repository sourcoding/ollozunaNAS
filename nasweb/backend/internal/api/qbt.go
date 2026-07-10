package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/nasweb/nasd/internal/qbt"
	"github.com/nasweb/nasd/internal/ws"
)

type qbtHandlers struct {
	db  *sql.DB
	mgr *qbt.Manager
	hub *ws.Hub
}

type qbtConfig struct {
	Configured   bool   `json:"configured"`
	TempDir      string `json:"temp_dir"`
	TorfileDir   string `json:"torfile_dir"`
	DownloadsDir string `json:"downloads_dir"`
	PermsMode    string `json:"perms_mode"`
	WebUIPort    int    `json:"webui_port"`
}

func (h *qbtHandlers) loadConfig() qbtConfig {
	var c qbtConfig
	var conf int
	_ = h.db.QueryRow(
		`SELECT configured, temp_dir, torfile_dir, downloads_dir, perms_mode, webui_port FROM qbt_config WHERE id=1`,
	).Scan(&conf, &c.TempDir, &c.TorfileDir, &c.DownloadsDir, &c.PermsMode, &c.WebUIPort)
	c.Configured = conf == 1
	if c.WebUIPort == 0 {
		c.WebUIPort = 8080
	}
	return c
}

// status restituisce lo stato della macchina a stati + la config corrente.
func (h *qbtHandlers) status(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadConfig()
	state := h.mgr.State(r.Context(), cfg.Configured)
	writeJSON(w, http.StatusOK, map[string]any{
		"state":      state,
		"configured": cfg.Configured,
		"config":     cfg,
		"webui_port": cfg.WebUIPort,
		"volumes":    len(h.mgr.Volumes(r.Context())),
	})
}

// volumes elenca i volumi dati montati (sorgente del gating).
func (h *qbtHandlers) volumes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.mgr.Volumes(r.Context()))
}

// browse elenca sottodirectory dentro un volume (o i volumi se path vuoto).
func (h *qbtHandlers) browse(w http.ResponseWriter, r *http.Request) {
	out, err := h.mgr.Browse(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// mkdir crea una cartella dentro un volume.
func (h *qbtHandlers) mkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	dst, err := h.mgr.Mkdir(r.Context(), req.Parent, req.Name)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": dst})
}

// validate controlla le 3 directory scelte.
func (h *qbtHandlers) validate(w http.ResponseWriter, r *http.Request) {
	var c qbt.Config
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	writeJSON(w, http.StatusOK, h.mgr.Validate(r.Context(), c.TempDir, c.TorfileDir, c.DownloadsDir))
}

// configure applica la configurazione completa e persiste su DB.
func (h *qbtHandlers) configure(w http.ResponseWriter, r *http.Request) {
	var c qbt.Config
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if c.WebUIPort == 0 {
		c.WebUIPort = 8080
	}
	mode := c.PermsMode
	if mode != "2775" {
		mode = "2770"
	}
	c.PermsMode = mode
	steps, pass, err := h.mgr.Configure(r.Context(), c)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "steps": steps})
		return
	}
	if _, e := h.db.ExecContext(r.Context(),
		`UPDATE qbt_config SET configured=1, temp_dir=?, torfile_dir=?, downloads_dir=?, perms_mode=?, webui_port=?, updated_at=CURRENT_TIMESTAMP WHERE id=1`,
		c.TempDir, c.TorfileDir, c.DownloadsDir, mode, c.WebUIPort,
	); e != nil {
		writeErr(w, http.StatusInternalServerError, e.Error())
		return
	}
	h.hub.Emit("qbt.status", map[string]string{"state": "running"})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "configured", "steps": steps,
		"webui_password": pass, "webui_port": c.WebUIPort,
	})
}

func (h *qbtHandlers) start(w http.ResponseWriter, r *http.Request) {
	if err := h.mgr.Start(r.Context()); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("qbt.status", map[string]string{"state": "running"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "running"})
}

func (h *qbtHandlers) stop(w http.ResponseWriter, r *http.Request) {
	if err := h.mgr.Stop(r.Context()); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("qbt.status", map[string]string{"state": "stopped"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}
