package system

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Management raccoglie le operazioni amministrative di sistema (spegnimento,
// riavvio, lettura log). Passa per un Runner così da restare testabile.
type Management struct {
	run Runner
}

func NewManagement(r Runner) *Management { return &Management{run: r} }

// LogEntry è una riga di log normalizzata per la UI.
type LogEntry struct {
	Time    string `json:"time"`
	Unit    string `json:"unit"`
	Message string `json:"message"`
	Level   string `json:"level"`
}

// Reboot riavvia la macchina. Il comando viene schedulato con un breve ritardo
// così l'handler HTTP può rispondere prima che la connessione cada.
func (m *Management) Reboot(ctx context.Context) error {
	// systemctl --no-block ritorna subito; la transizione avviene in background.
	_, _, err := m.run.Run(ctx, "systemctl", "--no-block", "reboot")
	return err
}

// Shutdown spegne la macchina (poweroff), sempre in modalità non bloccante.
func (m *Management) Shutdown(ctx context.Context) error {
	_, _, err := m.run.Run(ctx, "systemctl", "--no-block", "poweroff")
	return err
}

// Logs legge le ultime `lines` righe del journal. Se `unit` è valorizzato,
// filtra su quella unit systemd (es. "nasd", "smbd", "qbittorrent").
func (m *Management) Logs(ctx context.Context, unit string, lines int) ([]LogEntry, error) {
	if lines <= 0 || lines > 2000 {
		lines = 300
	}
	args := []string{
		"--no-pager",
		"-o", "short-iso",
		"-n", strconv.Itoa(lines),
	}
	if unit = strings.TrimSpace(unit); unit != "" {
		if !validUnit(unit) {
			return nil, fmt.Errorf("invalid unit")
		}
		args = append(args, "-u", unit)
	}
	out, _, err := m.run.Run(ctx, "journalctl", args...)
	if err != nil {
		return nil, err
	}
	return parseJournal(out), nil
}

// KnownUnits elenca le unit systemd rilevanti che la UI può filtrare.
func KnownUnits() []string {
	return []string{"nasd", "smbd", "nmbd", "nfs-server", "minidlna", "qbittorrent", "ssh"}
}

// isJournalTS riconosce il timestamp iniziale prodotto da journalctl -o
// short-iso. A seconda della versione di systemd l'offset del fuso può essere
// "+0200" oppure "+02:00": accettiamo entrambi.
func isJournalTS(ts string) bool {
	for _, layout := range []string{"2006-01-02T15:04:05-07:00", "2006-01-02T15:04:05-0700"} {
		if _, err := time.Parse(layout, ts); err == nil {
			return true
		}
	}
	return false
}

func validUnit(s string) bool {
	for _, u := range KnownUnits() {
		if s == u {
			return true
		}
	}
	return false
}

// parseJournal trasforma l'output "short-iso" di journalctl in LogEntry.
// Formato riga: "2026-07-10T12:00:00+0000 host unit[pid]: message".
func parseJournal(out string) []LogEntry {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	entries := make([]LogEntry, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" || strings.HasPrefix(ln, "-- ") {
			continue
		}
		e := LogEntry{Message: ln, Level: "info"}
		// timestamp = primo campo
		if sp := strings.IndexByte(ln, ' '); sp > 0 {
			ts := ln[:sp]
			if isJournalTS(ts) {
				e.Time = ts
				rest := strings.TrimSpace(ln[sp+1:])
				// rest = "host unit[pid]: message" → salta l'host
				if sp2 := strings.IndexByte(rest, ' '); sp2 > 0 {
					rest = rest[sp2+1:]
				}
				// separa "unit[pid]:" dal messaggio
				if colon := strings.IndexByte(rest, ':'); colon > 0 {
					tag := rest[:colon]
					if br := strings.IndexByte(tag, '['); br > 0 {
						tag = tag[:br]
					}
					e.Unit = tag
					e.Message = strings.TrimSpace(rest[colon+1:])
				} else {
					e.Message = rest
				}
			}
		}
		low := strings.ToLower(e.Message)
		switch {
		case strings.Contains(low, "error") || strings.Contains(low, "failed") || strings.Contains(low, "fatal"):
			e.Level = "error"
		case strings.Contains(low, "warn"):
			e.Level = "warn"
		}
		entries = append(entries, e)
	}
	return entries
}
