package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nasweb/nasd/internal/auth"
	"github.com/nasweb/nasd/internal/config"
	"github.com/nasweb/nasd/internal/dlna"
	"github.com/nasweb/nasd/internal/filemgr"
	"github.com/nasweb/nasd/internal/middleware"
	"github.com/nasweb/nasd/internal/raid"
	"github.com/nasweb/nasd/internal/shares"
	"github.com/nasweb/nasd/internal/system"
	"github.com/nasweb/nasd/internal/users"
	"github.com/nasweb/nasd/internal/ws"
)

// ---------------------------- AUTH -----------------------------------------

type authHandlers struct {
	cfg      *config.Config
	db       *sql.DB
	sessions *auth.SessionStore
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *authHandlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}

	// Verifica credenziali contro la tabella app_users (account dell'interfaccia,
	// distinti dagli utenti di sistema gestiti via PAM).
	var hash string
	var isAdmin bool
	err := h.db.QueryRow(
		`SELECT password_hash, is_admin FROM app_users WHERE username=?`,
		req.Username,
	).Scan(&hash, &isAdmin)
	if err != nil || !auth.CheckPassword(hash, req.Password) {
		// Messaggio generico per non rivelare se l'utente esiste.
		writeErr(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}

	sess := h.sessions.Create(req.Username, isAdmin)
	http.SetCookie(w, &http.Cookie{
		Name:     "nasd_session",
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.cfg.Server.TLSCert != "",
		SameSite: http.SameSiteStrictMode,
		Expires:  sess.ExpiresAt,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"username":   sess.Username,
		"is_admin":   sess.IsAdmin,
		"csrf_token": sess.CSRFToken,
	})
}

func (h *authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	if sess := middleware.SessionFrom(r); sess != nil {
		h.sessions.Delete(sess.Token)
	}
	http.SetCookie(w, &http.Cookie{
		Name: "nasd_session", Value: "", Path: "/",
		Expires: time.Unix(0, 0), HttpOnly: true,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *authHandlers) me(w http.ResponseWriter, r *http.Request) {
	sess := middleware.SessionFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":   sess.Username,
		"is_admin":   sess.IsAdmin,
		"csrf_token": sess.CSRFToken,
	})
}

// ---------------------------- USERS ----------------------------------------

type userHandlers struct {
	mgr *users.Manager
	db  *sql.DB
}

// appUser is the API representation of a web UI user.
type appUser struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	IsAdmin   bool   `json:"is_admin"`
	CreatedAt string `json:"created_at"`
}

func (h *userHandlers) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, username, is_admin, created_at FROM app_users ORDER BY id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	var result []appUser
	for rows.Next() {
		var u appUser
		if err := rows.Scan(&u.ID, &u.Username, &u.IsAdmin, &u.CreatedAt); err != nil {
			continue
		}
		result = append(result, u)
	}
	if result == nil {
		result = []appUser{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *userHandlers) listGroups(w http.ResponseWriter, r *http.Request) {
	g, err := h.mgr.ListGroups()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (h *userHandlers) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Comment  string `json:"comment"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if req.Password == "" {
		writeErr(w, http.StatusBadRequest, "password_required")
		return
	}
	// Utente di sistema per accesso NFS/Samba: crealo se non esiste (best-effort)
	// e imposta SEMPRE la password, che sincronizza anche quella Samba (smbpasswd),
	// necessaria per l'accesso CIFS. Anche quando l'account di sistema esisteva già
	// (es. "admin"): prima `CreateUser` falliva e la password Samba non veniva mai
	// impostata. Il login web resta comunque disponibile se questi passi falliscono.
	_ = h.mgr.CreateUser(r.Context(), req.Username, req.Comment)
	_ = h.mgr.SetPassword(r.Context(), req.Username, req.Password)

	// Insert into app_users so the user can log into the web UI.
	hash, err := auth.HashPassword(req.Password, 12)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash_error")
		return
	}
	isAdmin := 0
	if req.IsAdmin {
		isAdmin = 1
	}
	if _, err := h.db.ExecContext(r.Context(),
		`INSERT INTO app_users (username, password_hash, is_admin) VALUES (?, ?, ?)`,
		req.Username, hash, isAdmin,
	); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (h *userHandlers) setPassword(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := h.mgr.SetPassword(r.Context(), username, req.Password); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password, 12)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash_error")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE app_users SET password_hash=? WHERE username=?`, hash, username,
	); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *userHandlers) delete(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	// Remove from web UI accounts first.
	if _, err := h.db.ExecContext(r.Context(),
		`DELETE FROM app_users WHERE username=?`, username,
	); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Also delete Linux system user (ignore error if it doesn't exist).
	_ = h.mgr.DeleteUser(r.Context(), username, false)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------- RAID -----------------------------------------

type raidHandlers struct {
	mgr *raid.Manager
	hub *ws.Hub
}

func (h *raidHandlers) list(w http.ResponseWriter, r *http.Request) {
	arrays, err := h.mgr.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, arrays)
}

func (h *raidHandlers) listDisks(w http.ResponseWriter, r *http.Request) {
	disks, err := h.mgr.ListDisks(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, disks)
}

func (h *raidHandlers) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Device  string   `json:"device"`
		Level   string   `json:"level"`
		Devices []string `json:"devices"`
		Confirm bool     `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	if err := h.mgr.Create(r.Context(), req.Device, raid.Level(req.Level), req.Devices); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("raid.status", map[string]string{"device": req.Device, "state": "creating"})
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (h *raidHandlers) deleteArray(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("confirm") != "true" {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	md := "/dev/" + chi.URLParam(r, "md")
	if err := h.mgr.Stop(r.Context(), md); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("raid.status", map[string]string{"device": md, "state": "deleted"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *raidHandlers) formatArray(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FSType  string `json:"fstype"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	md := "/dev/" + chi.URLParam(r, "md")
	if err := h.mgr.Format(r.Context(), md, req.FSType); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("raid.status", map[string]string{"device": md, "state": "formatted"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "formatted", "fstype": req.FSType})
}

func (h *raidHandlers) wipeDisk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if !req.Confirm {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	disk := "/dev/" + chi.URLParam(r, "device")
	if err := h.mgr.WipeDisk(r.Context(), disk); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("raid.status", map[string]string{"device": disk, "state": "wiped"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "wiped"})
}

func (h *raidHandlers) deleteFilesystem(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("confirm") != "true" {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	md := "/dev/" + chi.URLParam(r, "md")
	if err := h.mgr.WipeFilesystem(r.Context(), md); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("raid.status", map[string]string{"device": md, "state": "fs_deleted"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *raidHandlers) listFilesystems(w http.ResponseWriter, r *http.Request) {
	fsList, err := h.mgr.ListFilesystems(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fsList)
}

func (h *raidHandlers) createFilesystem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FSType     string `json:"fstype"`
		MountPoint string `json:"mount_point"`
		Confirm    bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	md := "/dev/" + chi.URLParam(r, "md")
	if err := h.mgr.CreateFilesystem(r.Context(), md, req.FSType, req.MountPoint); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("raid.status", map[string]string{"device": md, "state": "mounted"})
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created", "mount_point": req.MountPoint})
}

func (h *raidHandlers) unmountFilesystem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MountPoint string `json:"mount_point"`
		Confirm    bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	md := "/dev/" + chi.URLParam(r, "md")
	if err := h.mgr.UnmountFilesystem(r.Context(), req.MountPoint); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("raid.status", map[string]string{"device": md, "state": "unmounted"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "unmounted"})
}

func (h *raidHandlers) mountFilesystem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MountPoint string `json:"mount_point"`
		FSType     string `json:"fstype"`
		Confirm    bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	md := "/dev/" + chi.URLParam(r, "md")
	if err := h.mgr.MountOnly(r.Context(), md, req.FSType, req.MountPoint); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("raid.status", map[string]string{"device": md, "state": "mounted"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "mounted", "mount_point": req.MountPoint})
}

func (h *raidHandlers) growFilesystem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FSType string `json:"fstype"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	md := "/dev/" + chi.URLParam(r, "md")
	if err := h.mgr.GrowFilesystem(r.Context(), md, req.FSType); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("raid.status", map[string]string{"device": md, "state": "grown"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "grown"})
}

func (h *raidHandlers) smart(w http.ResponseWriter, r *http.Request) {
	device := "/dev/" + chi.URLParam(r, "device")
	info, err := h.mgr.SmartInfo(r.Context(), device)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *raidHandlers) addDisk(w http.ResponseWriter, r *http.Request) {
	md := "/dev/" + chi.URLParam(r, "md")
	var req struct {
		Disk string `json:"disk"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := h.mgr.AddDisk(r.Context(), md, req.Disk); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

func (h *raidHandlers) removeDisk(w http.ResponseWriter, r *http.Request) {
	md := "/dev/" + chi.URLParam(r, "md")
	disk := "/dev/" + chi.URLParam(r, "disk")
	if err := h.mgr.RemoveDisk(r.Context(), md, disk); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// ---------------------------- SHARES ---------------------------------------

// La tabella `shares` è la sorgente di verità: dopo ogni mutazione si rigenerano
// /etc/exports e l'include Samba dalla lista completa (shares.Manager.Apply*).
type sharesHandlers struct {
	db  *sql.DB
	mgr *shares.Manager
	hub *ws.Hub
}

// shareDTO è la rappresentazione API di una share: la Share di dominio più l'id
// del DB, necessario al client per update/delete.
type shareDTO struct {
	ID int `json:"id"`
	shares.Share
}

func (h *sharesHandlers) list(w http.ResponseWriter, r *http.Request) {
	all, err := h.loadAll()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, all)
}

// isUniqueViolation riconosce la violazione del vincolo UNIQUE(name, protocol)
// così da restituire un 409 con messaggio comprensibile invece dell'errore
// SQLite grezzo. Match sul testo per non dipendere dal driver specifico.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (h *sharesHandlers) create(w http.ResponseWriter, r *http.Request) {
	var req shares.Share
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := validateShare(req); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	_, err := h.db.Exec(
		`INSERT INTO shares(name, path, protocol, read_only, allowed_ips, valid_users, enabled, config)
		 VALUES(?,?,?,?,?,?,?,?)`,
		req.Name, req.Path, string(req.Protocol), boolToInt(req.ReadOnly),
		joinCSV(req.AllowedIPs), joinCSV(req.ValidUsers), boolToInt(req.Enabled), configStr(req.Config),
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeErr(w, http.StatusConflict, fmt.Sprintf("esiste già una share '%s' con protocollo %s", req.Name, req.Protocol))
			return
		}
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := h.reapply(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "apply_failed: "+err.Error())
		return
	}
	h.hub.Emit("shares.changed", map[string]string{"name": req.Name, "action": "created"})
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (h *sharesHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id")
		return
	}
	var req struct {
		shares.Share
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := validateShare(req.Share); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Disabilitare una share interrompe l'accesso ai client: lo trattiamo come
	// operazione disruptiva e richiediamo conferma esplicita.
	if !req.Enabled && !req.Confirm {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	res, err := h.db.Exec(
		`UPDATE shares SET name=?, path=?, protocol=?, read_only=?, allowed_ips=?, valid_users=?, enabled=?, config=?
		 WHERE id=?`,
		req.Name, req.Path, string(req.Protocol), boolToInt(req.ReadOnly),
		joinCSV(req.AllowedIPs), joinCSV(req.ValidUsers), boolToInt(req.Enabled), configStr(req.Config), id,
	)
	if err != nil {
		if isUniqueViolation(err) {
			writeErr(w, http.StatusConflict, fmt.Sprintf("esiste già una share '%s' con protocollo %s", req.Name, req.Protocol))
			return
		}
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "not_found")
		return
	}
	if err := h.reapply(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "apply_failed: "+err.Error())
		return
	}
	h.hub.Emit("shares.changed", map[string]string{"name": req.Name, "action": "updated"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *sharesHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id")
		return
	}
	// L'eliminazione è distruttiva: richiede ?confirm=true.
	if r.URL.Query().Get("confirm") != "true" {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	res, err := h.db.Exec(`DELETE FROM shares WHERE id=?`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "not_found")
		return
	}
	if err := h.reapply(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "apply_failed: "+err.Error())
		return
	}
	h.hub.Emit("shares.changed", map[string]any{"id": id, "action": "deleted"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// reapply rigenera /etc/exports e la config Samba dall'intera tabella shares.
func (h *sharesHandlers) reapply(ctx context.Context) error {
	all, err := h.loadAll()
	if err != nil {
		return err
	}
	list := make([]shares.Share, len(all))
	for i, s := range all {
		list[i] = s.Share
	}
	if err := h.mgr.ApplyNFS(ctx, list); err != nil {
		return err
	}
	return h.mgr.ApplySMB(ctx, list)
}

func (h *sharesHandlers) loadAll() ([]shareDTO, error) {
	rows, err := h.db.Query(
		`SELECT id, name, path, protocol, read_only, allowed_ips, valid_users, enabled, config
		 FROM shares ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []shareDTO{}
	for rows.Next() {
		var s shareDTO
		var proto, ips, vusers, cfg string
		var ro, en int
		if err := rows.Scan(&s.ID, &s.Name, &s.Path, &proto, &ro, &ips, &vusers, &en, &cfg); err != nil {
			return nil, err
		}
		s.Protocol = shares.Protocol(proto)
		s.ReadOnly = ro != 0
		s.Enabled = en != 0
		s.AllowedIPs = splitCSV(ips)
		s.ValidUsers = splitCSV(vusers)
		if cfg != "" {
			s.Config = json.RawMessage(cfg)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// validateShare applica le stesse regole dei generatori di config (nome share,
// percorso assoluto senza caratteri pericolosi) prima di toccare il DB.
func validateShare(s shares.Share) error {
	if !system.ValidShareName(s.Name) {
		return fmt.Errorf("nome share non valido")
	}
	if s.Protocol != shares.NFS && s.Protocol != shares.SMB {
		return fmt.Errorf("protocollo non valido")
	}
	if !strings.HasPrefix(s.Path, "/") || strings.ContainsAny(s.Path, "\n\r\"\x00") {
		return fmt.Errorf("percorso non valido")
	}
	return nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinCSV(xs []string) string { return strings.Join(xs, ",") }

// configStr serializza il blob di configurazione avanzata della share per il DB
// (stringa vuota se assente).
func configStr(c json.RawMessage) string {
	if len(c) == 0 {
		return ""
	}
	return string(c)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------------------------- FILE MANAGER ---------------------------------

// fileHandlers espone il file manager via browser. Tutte le operazioni sono
// confinate sotto la root configurata (filemgr.Manager fa da jail anti
// path-traversal). Per ora sono riservate agli admin: lo scoping per-utente è
// lavoro futuro (manca la mappatura utente→percorsi consentiti).
type fileHandlers struct {
	mgr *filemgr.Manager
	hub *ws.Hub
}

func (h *fileHandlers) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.mgr.List(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (h *fileHandlers) mkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := h.mgr.Mkdir(req.Path); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (h *fileHandlers) rename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := h.mgr.Rename(req.Src, req.Dst); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "renamed"})
}

func (h *fileHandlers) remove(w http.ResponseWriter, r *http.Request) {
	// Eliminazione distruttiva (ricorsiva): richiede ?confirm=true.
	if r.URL.Query().Get("confirm") != "true" {
		writeErr(w, http.StatusPreconditionRequired, "confirmation_required")
		return
	}
	if err := h.mgr.Remove(r.URL.Query().Get("path")); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *fileHandlers) chmod(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Mode string `json:"mode"` // ottale, es. "755"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	m, err := strconv.ParseUint(req.Mode, 8, 32)
	if err != nil || m > 0o777 {
		writeErr(w, http.StatusUnprocessableEntity, "mode_non_valido")
		return
	}
	if err := h.mgr.Chmod(req.Path, os.FileMode(m)); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "chmod_ok"})
}

func (h *fileHandlers) download(w http.ResponseWriter, r *http.Request) {
	f, info, err := h.mgr.OpenForDownload(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", info.Name()))
	_, _ = io.Copy(w, f) // streaming: non carica il file in memoria
}

// upload riceve file via multipart in streaming (nessun buffer in memoria) e li
// salva nella directory ?path=. L'avanzamento è notificato via WebSocket
// (evento file.progress) per alimentare la progress bar del frontend.
func (h *fileHandlers) upload(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_multipart")
		return
	}
	var saved []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_part")
			return
		}
		if part.FileName() == "" {
			continue // campo non-file
		}
		name := filepath.Base(part.FileName()) // scarta eventuali separatori nel nome
		dest := filepath.Join(dir, name)
		pr := &progressReader{src: part, name: name, hub: h.hub}
		if _, err := h.mgr.Save(dest, pr); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		h.hub.Emit("file.progress", map[string]any{"name": name, "done": true})
		saved = append(saved, name)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "uploaded", "files": saved})
}

// progressReader conta i byte letti e notifica l'avanzamento via WebSocket a
// intervalli (ogni ~1 MiB) per non inondare il canale.
type progressReader struct {
	src      io.Reader
	name     string
	hub      *ws.Hub
	read     int64
	lastEmit int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.src.Read(b)
	p.read += int64(n)
	if p.read-p.lastEmit >= 1<<20 { // 1 MiB
		p.lastEmit = p.read
		p.hub.Emit("file.progress", map[string]any{"name": p.name, "bytes": p.read})
	}
	return n, err
}

// ---------------------------- DLNA -----------------------------------------

// La tabella media_dirs è la sorgente di verità delle cartelle multimediali:
// dopo ogni mutazione si rigenera minidlna.conf (dlna.Manager.Configure) usando
// come friendly_name il valore di settings.nas_name.
type dlnaHandlers struct {
	db  *sql.DB
	mgr *dlna.Manager
	hub *ws.Hub
}

func (h *dlnaHandlers) listDirs(w http.ResponseWriter, r *http.Request) {
	dirs, err := h.loadDirs()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dirs)
}

// mediaExt classifica un file per estensione: V video, P immagini, A audio, "" altro.
func mediaExt(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mp4", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".m4v", ".mpg", ".mpeg", ".webm", ".ts", ".m2ts":
		return "V"
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tiff", ".tif", ".webp", ".heic":
		return "P"
	case ".mp3", ".flac", ".wav", ".aac", ".ogg", ".m4a", ".wma", ".opus":
		return "A"
	}
	return ""
}

// listFiles percorre le cartelle multimediali configurate e restituisce i file
// raggruppati per categoria (V/P/A), classificati per estensione.
func (h *dlnaHandlers) listFiles(w http.ResponseWriter, r *http.Request) {
	dirs, err := h.loadDirs()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type mediaFile struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Size int64  `json:"size"`
		Dir  string `json:"dir"`
	}
	const maxPerType = 1000
	result := map[string][]mediaFile{"V": {}, "P": {}, "A": {}}
	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d.Path] {
			continue
		}
		seen[d.Path] = true
		_ = filepath.Walk(d.Path, func(p string, info os.FileInfo, werr error) error {
			if werr != nil || info == nil || info.IsDir() {
				return nil
			}
			cat := mediaExt(info.Name())
			if cat == "" || len(result[cat]) >= maxPerType {
				return nil
			}
			result[cat] = append(result[cat], mediaFile{Name: info.Name(), Path: p, Size: info.Size(), Dir: d.Path})
			return nil
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *dlnaHandlers) addDir(w http.ResponseWriter, r *http.Request) {
	var req dlna.MediaDir
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !strings.HasPrefix(req.Path, "/") || strings.ContainsAny(req.Path, "\n\r\x00") {
		writeErr(w, http.StatusUnprocessableEntity, "percorso non valido")
		return
	}
	switch req.Type {
	case "", "A", "V", "P": // tutti, Audio, Video, immagini (P=pictures)
	default:
		writeErr(w, http.StatusUnprocessableEntity, "tipo media non valido")
		return
	}
	if _, err := h.db.Exec(`INSERT INTO media_dirs(path, media_type) VALUES(?,?)`, req.Path, req.Type); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := h.reconfigure(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "apply_failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "added"})
}

func (h *dlnaHandlers) removeDir(w http.ResponseWriter, r *http.Request) {
	res, err := h.db.Exec(`DELETE FROM media_dirs WHERE path=?`, r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "not_found")
		return
	}
	if err := h.reconfigure(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "apply_failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *dlnaHandlers) rescan(w http.ResponseWriter, r *http.Request) {
	if err := h.mgr.Rescan(r.Context()); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.hub.Emit("dlna.rescan", map[string]string{"status": "started"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "rescanning"})
}

// reconfigure rigenera minidlna.conf dall'intera tabella media_dirs.
func (h *dlnaHandlers) reconfigure(ctx context.Context) error {
	dirs, err := h.loadDirs()
	if err != nil {
		return err
	}
	name := "NAS"
	_ = h.db.QueryRow(`SELECT value FROM settings WHERE key='nas_name'`).Scan(&name)
	return h.mgr.Configure(ctx, name, dirs)
}

func (h *dlnaHandlers) loadDirs() ([]dlna.MediaDir, error) {
	rows, err := h.db.Query(`SELECT path, media_type FROM media_dirs ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dlna.MediaDir{}
	for rows.Next() {
		var d dlna.MediaDir
		if err := rows.Scan(&d.Path, &d.Type); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
