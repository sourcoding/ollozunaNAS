// Package dlna gestisce il server DLNA/UPnP tramite MiniDLNA: configurazione
// delle cartelle multimediali, riavvio del servizio e re-scan delle librerie.
package dlna

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nasweb/nasd/internal/system"
)

// MediaDir è una cartella multimediale condivisa con un tipo media MiniDLNA.
type MediaDir struct {
	Path string `json:"path"`
	Type string `json:"type"` // "A" audio, "V" video, "P" immagini, "" tutti
}

type Manager struct {
	run      system.Runner
	confPath string
}

func NewManager(run system.Runner, confPath string) *Manager {
	return &Manager{run: run, confPath: confPath}
}

// Configure rigenera minidlna.conf con le cartelle indicate e riavvia il servizio.
func (m *Manager) Configure(ctx context.Context, name string, dirs []MediaDir) error {
	var b strings.Builder
	b.WriteString("# Generato da nasd\n")
	b.WriteString(fmt.Sprintf("friendly_name=%s\n", sanitize(name)))
	b.WriteString("db_dir=/var/cache/minidlna\n")
	b.WriteString("log_dir=/var/log\n")
	b.WriteString("inotify=yes\n")
	for _, d := range dirs {
		if strings.ContainsAny(d.Path, "\n") {
			return fmt.Errorf("percorso non valido")
		}
		if d.Type != "" {
			b.WriteString(fmt.Sprintf("media_dir=%s,%s\n", d.Type, d.Path))
		} else {
			b.WriteString(fmt.Sprintf("media_dir=%s\n", d.Path))
		}
	}
	if err := os.WriteFile(m.confPath, []byte(b.String()), 0o644); err != nil {
		return err
	}
	_, _, err := m.run.Run(ctx, "systemctl", "restart", "minidlna")
	return err
}

// Rescan forza la ricostruzione del catalogo media.
func (m *Manager) Rescan(ctx context.Context) error {
	_, _, err := m.run.Run(ctx, "minidlnad", "-R")
	if err != nil {
		return err
	}
	_, _, err = m.run.Run(ctx, "systemctl", "restart", "minidlna")
	return err
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s)
}
