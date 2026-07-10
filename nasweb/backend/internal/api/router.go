// Package api costruisce il router HTTP e collega gli handler ai moduli di
// dominio (users, shares, raid, filemgr, dlna).
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/nasweb/nasd/internal/auth"
	"github.com/nasweb/nasd/internal/config"
	"github.com/nasweb/nasd/internal/dlna"
	"github.com/nasweb/nasd/internal/filemgr"
	"github.com/nasweb/nasd/internal/middleware"
	"github.com/nasweb/nasd/internal/qbt"
	"github.com/nasweb/nasd/internal/raid"
	"github.com/nasweb/nasd/internal/shares"
	"github.com/nasweb/nasd/internal/system"
	"github.com/nasweb/nasd/internal/users"
	"github.com/nasweb/nasd/internal/ws"
)

// Deps raccoglie le dipendenze iniettate nel router.
type Deps struct {
	Config   *config.Config
	DB       *sql.DB
	Hub      *ws.Hub
	Sessions *auth.SessionStore
}

// NewRouter costruisce il chi.Router completo.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(middleware.Recover)
	r.Use(middleware.SecurityHeaders)

	runner := system.NewExecRunner()
	userMgr := users.NewManager(runner)
	raidMgr := raid.NewManager(runner)
	sharesMgr := shares.NewManager(runner, d.Config.Paths.Exports, d.Config.Paths.SambaConf)
	fileMgr := filemgr.NewManager(d.Config.Paths.FilesRoot)
	dlnaMgr := dlna.NewManager(runner, d.Config.Paths.MiniDLNAConf)

	authH := &authHandlers{cfg: d.Config, db: d.DB, sessions: d.Sessions}
	userH := &userHandlers{mgr: userMgr, db: d.DB}
	raidH := &raidHandlers{mgr: raidMgr, hub: d.Hub}
	sharesH := &sharesHandlers{db: d.DB, mgr: sharesMgr, hub: d.Hub}
	fileH := &fileHandlers{mgr: fileMgr, hub: d.Hub}
	dlnaH := &dlnaHandlers{db: d.DB, mgr: dlnaMgr, hub: d.Hub}

	// qBittorrent: i volumi dati provengono dai filesystem RAID montati (nessun
	// path hardcoded). L'adapter disaccoppia qbt da raid.
	qbtMgr := qbt.NewManager(runner, func(ctx context.Context) []qbt.Volume {
		fss, _ := raidMgr.ListFilesystems(ctx)
		out := make([]qbt.Volume, 0)
		for _, f := range fss {
			if !f.IsMounted || f.MountPoint == "" {
				continue
			}
			out = append(out, qbt.Volume{
				Name:       filepath.Base(f.MountPoint),
				MountPoint: f.MountPoint,
				FSType:     f.FSType,
				TotalBytes: f.TotalBytes,
				FreeBytes:  f.FreeBytes,
				Writable:   true,
			})
		}
		return out
	})
	qbtH := &qbtHandlers{db: d.DB, mgr: qbtMgr, hub: d.Hub}

	adminH := &adminHandlers{mgr: system.NewManagement(runner), hub: d.Hub}

	// --- API pubbliche (no auth) ---
	r.Route("/api", func(r chi.Router) {
		r.Post("/auth/login", authH.login)

		// --- API protette ---
		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(d.Sessions))
			r.Use(middleware.CSRF(d.Config.Security.CSRFEnabled))

			r.Post("/auth/logout", authH.logout)
			r.Get("/auth/me", authH.me)

			// Utenti/gruppi: solo admin per le mutazioni.
			r.Route("/users", func(r chi.Router) {
				r.Get("/", userH.list)
				r.With(middleware.RequireAdmin).Post("/", userH.create)
				r.With(middleware.RequireAdmin).Delete("/{username}", userH.delete)
				r.With(middleware.RequireAdmin).Post("/{username}/password", userH.setPassword)
			})
			r.Get("/groups", userH.listGroups)

			// RAID: tutte operazioni admin.
			r.Route("/raid", func(r chi.Router) {
				r.Use(middleware.RequireAdmin)
				r.Get("/disks", raidH.listDisks)
				r.Post("/disks/{device}/wipe", raidH.wipeDisk)
				r.Get("/arrays", raidH.list)
				r.Post("/arrays", raidH.create)
				r.Get("/disks/{device}/smart", raidH.smart)
				r.Delete("/arrays/{md}", raidH.deleteArray)
				r.Post("/arrays/{md}/disks", raidH.addDisk)
				r.Delete("/arrays/{md}/disks/{disk}", raidH.removeDisk)
				r.Get("/filesystems", raidH.listFilesystems)
				r.Post("/arrays/{md}/filesystem", raidH.createFilesystem)
				r.Post("/arrays/{md}/filesystem/mount", raidH.mountFilesystem)
				r.Post("/arrays/{md}/filesystem/unmount", raidH.unmountFilesystem)
				r.Post("/arrays/{md}/filesystem/grow", raidH.growFilesystem)
				r.Delete("/arrays/{md}/filesystem", raidH.deleteFilesystem)
			})

			// Share: lettura per tutti gli autenticati, mutazioni solo admin.
			// La tabella shares è la sorgente di verità da cui si rigenerano
			// /etc/exports e la config Samba a ogni modifica.
			r.Route("/shares", func(r chi.Router) {
				r.Get("/", sharesH.list)
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireAdmin)
					r.Post("/", sharesH.create)
					r.Put("/{id}", sharesH.update)
					r.Delete("/{id}", sharesH.delete)
				})
			})

			// File manager: confinato sotto FilesRoot. Riservato agli admin
			// finché non esiste lo scoping per-utente dei percorsi.
			r.Route("/files", func(r chi.Router) {
				r.Use(middleware.RequireAdmin)
				r.Get("/", fileH.list)
				r.Get("/download", fileH.download)
				r.Post("/mkdir", fileH.mkdir)
				r.Post("/rename", fileH.rename)
				r.Post("/chmod", fileH.chmod)
				r.Post("/upload", fileH.upload)
				r.Delete("/", fileH.remove)
			})

			// DLNA: cartelle multimediali (media_dirs) + rescan. Solo admin.
			r.Route("/dlna", func(r chi.Router) {
				r.Use(middleware.RequireAdmin)
				r.Get("/dirs", dlnaH.listDirs)
				r.Get("/files", dlnaH.listFiles)
				r.Post("/dirs", dlnaH.addDir)
				r.Delete("/dirs", dlnaH.removeDir)
				r.Post("/rescan", dlnaH.rescan)
			})

			// App qBittorrent (gating sui volumi + wizard nei prossimi incrementi).
			r.Route("/apps/qbt", func(r chi.Router) {
				r.Get("/status", qbtH.status)
				r.Get("/volumes", qbtH.volumes)
				r.With(middleware.RequireAdmin).Get("/browse", qbtH.browse)
				r.With(middleware.RequireAdmin).Post("/mkdir", qbtH.mkdir)
				r.With(middleware.RequireAdmin).Post("/validate", qbtH.validate)
				r.With(middleware.RequireAdmin).Post("/configure", qbtH.configure)
				r.With(middleware.RequireAdmin).Post("/start", qbtH.start)
				r.With(middleware.RequireAdmin).Post("/stop", qbtH.stop)
			})

			// Amministrazione sistema: log + spegnimento/riavvio. Solo admin.
			r.Route("/admin", func(r chi.Router) {
				r.Use(middleware.RequireAdmin)
				r.Get("/logs", adminH.logs)
				r.Post("/reboot", adminH.reboot)
				r.Post("/shutdown", adminH.shutdown)
			})
		})
	})

	// WebSocket (l'auth via cookie è verificata nell'handler).
	r.Get("/ws", d.Hub.ServeWS)

	// SPA statica con fallback su index.html per il client-side routing.
	staticDir := d.Config.Paths.StaticDir
	fs := http.FileServer(http.Dir(staticDir))
	r.Handle("/assets/*", fs)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
	})

	return r
}

// writeJSON è l'helper di risposta condiviso.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
