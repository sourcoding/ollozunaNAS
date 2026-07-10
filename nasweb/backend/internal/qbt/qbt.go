// Package qbt gestisce l'app qBittorrent (qbittorrent-nox nativo via systemd):
// stato (macchina a stati), volumi dati disponibili, validazione dei path dentro
// i volumi e (nei prossimi incrementi) operazioni privilegiate + config + servizio.
package qbt

import (
	"context"
	"strings"

	"github.com/nasweb/nasd/internal/system"
)

// Stati espliciti dell'app.
const (
	StateUnavailable = "unavailable" // nessun volume dati montato
	StateAvailable   = "available"   // volumi presenti ma app non configurata
	StateConfigured  = "configured"  // wizard completato (transitorio)
	StateRunning     = "running"
	StateStopped     = "stopped"
)

// ServiceUnit è l'unità systemd che nasd avvia/ferma per qBittorrent.
const ServiceUnit = "qbittorrent.service"

// Volume è un volume dati montato del NAS (sorgente: raid.ListFilesystems).
type Volume struct {
	Name       string `json:"name"`
	MountPoint string `json:"mount_point"`
	FSType     string `json:"fstype"`
	TotalBytes int64  `json:"total_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
	Writable   bool   `json:"writable"`
}

// VolumeLister fornisce i volumi dati correnti (montati). Iniettato dal router
// come adattatore su raid.ListFilesystems: evita path hardcoded e dipendenze cicliche.
type VolumeLister func(context.Context) []Volume

type Manager struct {
	run  system.Runner
	vols VolumeLister
}

func NewManager(run system.Runner, vols VolumeLister) *Manager {
	return &Manager{run: run, vols: vols}
}

// Volumes restituisce i volumi dati montati.
func (m *Manager) Volumes(ctx context.Context) []Volume { return m.vols(ctx) }

// State calcola lo stato dell'app dalla presenza di volumi, dal flag configured
// e dallo stato del servizio.
func (m *Manager) State(ctx context.Context, configured bool) string {
	if len(m.vols(ctx)) == 0 {
		return StateUnavailable
	}
	if !configured {
		return StateAvailable
	}
	if m.ServiceActive(ctx) {
		return StateRunning
	}
	return StateStopped
}

// ServiceActive indica se l'unità qBittorrent è attiva.
func (m *Manager) ServiceActive(ctx context.Context) bool {
	out, _, _ := m.run.Run(ctx, "systemctl", "is-active", ServiceUnit)
	return strings.TrimSpace(out) == "active"
}

// Reconcile applica la regola di gating a caldo: se non ci sono più volumi ma il
// servizio è attivo, lo ferma in modo pulito (l'app torna "unavailable"). Va
// chiamato quando cambiano i volumi (es. dopo mutazioni RAID/filesystem).
func (m *Manager) Reconcile(ctx context.Context) {
	if len(m.vols(ctx)) == 0 && m.ServiceActive(ctx) {
		m.run.Run(ctx, "systemctl", "stop", ServiceUnit) //nolint:errcheck
	}
}
