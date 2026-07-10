// Package shares gestisce le condivisioni di rete NFS (/etc/exports) e
// CIFS/SMB (Samba). Le configurazioni vengono validate prima dell'applicazione
// e i servizi ricaricati senza riavvio dove possibile (exportfs -ra, smbcontrol).
package shares

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nasweb/nasd/internal/system"
)

// Protocol distingue il tipo di share.
type Protocol string

const (
	NFS Protocol = "nfs"
	SMB Protocol = "smb"
)

// Share descrive una condivisione.
type Share struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Protocol   Protocol `json:"protocol"`
	ReadOnly   bool     `json:"read_only"`
	AllowedIPs []string `json:"allowed_ips"` // NFS: host/reti autorizzati
	ValidUsers []string `json:"valid_users"` // SMB: utenti/gruppi (@group)
	Enabled    bool     `json:"enabled"`
}

type Manager struct {
	run         system.Runner
	exportsPath string
	sambaConf   string
}

func NewManager(run system.Runner, exportsPath, sambaConf string) *Manager {
	return &Manager{run: run, exportsPath: exportsPath, sambaConf: sambaConf}
}

// ApplyNFS riscrive /etc/exports per le share NFS abilitate e ricarica.
// La generazione del file da una sorgente strutturata evita injection.
func (m *Manager) ApplyNFS(ctx context.Context, all []Share) error {
	var b strings.Builder
	b.WriteString("# Generato da nasd - non modificare a mano\n")
	enabled := 0
	for _, s := range all {
		if s.Protocol != NFS || !s.Enabled {
			continue
		}
		if strings.ContainsAny(s.Path, "\n\"") {
			return fmt.Errorf("percorso non valido: %s", s.Path)
		}
		if _, _, err := m.run.Run(ctx, "mkdir", "-p", s.Path); err != nil {
			return fmt.Errorf("creazione cartella %s: %w", s.Path, err)
		}
		m.run.Run(ctx, "chmod", "0777", s.Path) //nolint:errcheck
		enabled++
		opts := "rw"
		if s.ReadOnly {
			opts = "ro"
		}
		hosts := s.AllowedIPs
		if len(hosts) == 0 {
			hosts = []string{"*"}
		}
		// Il formato exports supporta percorsi tra virgolette (gestisce spazi).
		// Il controllo precedente ha già escluso newline e doppi apici.
		b.WriteString(fmt.Sprintf("%q", s.Path))
		for _, h := range hosts {
			b.WriteString(fmt.Sprintf(" %s(%s,sync,no_subtree_check)", h, opts))
		}
		b.WriteString("\n")
	}
	if err := writeFileAtomic(m.exportsPath, b.String(), 0o644); err != nil {
		return err
	}
	// Il server NFS è disabilitato di default: avvialo quando esiste almeno una
	// export abilitata (senza nfs-server `exportfs -ra` non basta a servire i
	// client). Se non ci sono export, fermalo per non tenere porte aperte.
	if enabled > 0 {
		if _, _, err := m.run.Run(ctx, "systemctl", "enable", "--now", "nfs-server"); err != nil {
			return fmt.Errorf("avvio nfs-server: %w", err)
		}
	} else {
		m.run.Run(ctx, "systemctl", "disable", "--now", "nfs-server") //nolint:errcheck
	}
	_, _, err := m.run.Run(ctx, "exportfs", "-ra")
	return err
}

// ApplySMB rigenera la sezione delle share nel file Samba e ricarica.
// In produzione conviene usare un include dedicato (es. /etc/samba/shares.conf)
// per non toccare la configurazione globale dell'amministratore.
func (m *Manager) ApplySMB(ctx context.Context, all []Share) error {
	var b strings.Builder
	b.WriteString("# Generato da nasd\n")
	enabled := 0
	for _, s := range all {
		if s.Protocol != SMB || !s.Enabled {
			continue
		}
		if !system.ValidShareName(s.Name) {
			return fmt.Errorf("nome share non valido: %s", s.Name)
		}
		if _, _, err := m.run.Run(ctx, "mkdir", "-p", s.Path); err != nil {
			return fmt.Errorf("creazione cartella %s: %w", s.Path, err)
		}
		m.run.Run(ctx, "chmod", "0777", s.Path) //nolint:errcheck
		enabled++
		b.WriteString(fmt.Sprintf("[%s]\n", s.Name))
		b.WriteString(fmt.Sprintf("   path = %s\n", s.Path))
		b.WriteString(fmt.Sprintf("   read only = %v\n", s.ReadOnly))
		if len(s.ValidUsers) > 0 {
			// Share autenticata: solo gli utenti/gruppi indicati (serve password
			// Samba, impostata da users.SetPassword via smbpasswd).
			b.WriteString(fmt.Sprintf("   valid users = %s\n", strings.Join(s.ValidUsers, " ")))
		} else {
			// Nessun utente indicato: share pubblica (guest). Richiede
			// "map to guest = Bad User" in [global] (impostato in fase di build).
			b.WriteString("   guest ok = yes\n")
		}
		b.WriteString("   create mask = 0664\n")
		b.WriteString("   directory mask = 0775\n")
		b.WriteString("   browseable = yes\n\n")
	}
	// L'include vive accanto a smb.conf (es. /etc/samba/nasd-shares.conf), così
	// non si tocca la config globale dell'amministratore ed è configurabile/testabile.
	includePath := filepath.Join(filepath.Dir(m.sambaConf), "nasd-shares.conf")
	if err := writeFileAtomic(includePath, b.String(), 0o644); err != nil {
		return err
	}
	// Validazione con testparm prima di ricaricare.
	if _, _, err := m.run.Run(ctx, "testparm", "-s", m.sambaConf); err != nil {
		return fmt.Errorf("configurazione samba non valida: %w", err)
	}
	// smbd/nmbd sono disabilitati di default: avviali quando c'è almeno una
	// share SMB abilitata, altrimenti fermali. reload-config è best-effort
	// (fallisce se smbd non è in esecuzione, ma dopo enable --now lo è già).
	if enabled > 0 {
		if _, _, err := m.run.Run(ctx, "systemctl", "enable", "--now", "smbd", "nmbd"); err != nil {
			return fmt.Errorf("avvio smbd/nmbd: %w", err)
		}
		m.run.Run(ctx, "smbcontrol", "all", "reload-config") //nolint:errcheck
	} else {
		m.run.Run(ctx, "systemctl", "disable", "--now", "smbd", "nmbd") //nolint:errcheck
	}
	return nil
}

func writeFileAtomic(path, content string, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
