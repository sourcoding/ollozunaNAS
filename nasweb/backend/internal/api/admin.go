package api

import (
	"net/http"
	"strconv"

	"github.com/nasweb/nasd/internal/system"
	"github.com/nasweb/nasd/internal/ws"
)

// adminHandlers espone le operazioni amministrative di sistema: spegnimento,
// riavvio e lettura dei log. Tutte riservate agli admin dal router.
type adminHandlers struct {
	mgr *system.Management
	hub *ws.Hub
}

// logs restituisce le ultime righe del journal, opzionalmente filtrate per unit.
func (h *adminHandlers) logs(w http.ResponseWriter, r *http.Request) {
	unit := r.URL.Query().Get("unit")
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	out, err := h.mgr.Logs(r.Context(), unit, lines)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"units":   system.KnownUnits(),
		"entries": out,
	})
}

// reboot pianifica il riavvio della macchina.
func (h *adminHandlers) reboot(w http.ResponseWriter, r *http.Request) {
	if h.hub != nil {
		h.hub.Emit("system.power", map[string]string{"action": "reboot"})
	}
	if err := h.mgr.Reboot(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rebooting"})
}

// shutdown pianifica lo spegnimento della macchina.
func (h *adminHandlers) shutdown(w http.ResponseWriter, r *http.Request) {
	if h.hub != nil {
		h.hub.Emit("system.power", map[string]string{"action": "shutdown"})
	}
	if err := h.mgr.Shutdown(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting_down"})
}
