// Package shares gestisce le condivisioni di rete NFS (/etc/exports) e
// CIFS/SMB (Samba). Le configurazioni vengono validate prima dell'applicazione
// e i servizi ricaricati senza riavvio dove possibile (exportfs -ra, smbcontrol).
package shares

import (
	"context"
	"encoding/json"
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
	// Config è la configurazione avanzata (opzioni SMB/NFS) prodotta dalla UI,
	// conservata come blob JSON e riapplicata. Round-trip completo: tutto ciò che
	// l'utente imposta viene salvato e riletto senza perdite.
	Config json.RawMessage `json:"config,omitempty"`
}

// ShareConfig è la vista tipizzata (parziale) del blob Config usata dai
// generatori di config. Rispecchia la forma inviata dal frontend.
type ShareConfig struct {
	RecycleBin bool        `json:"recycleBin"`
	SMB        SMBOptions  `json:"smb"`
	Rules      []NFSRule   `json:"rules"`
	Versions   NFSVersions `json:"versions"`
}

// SMBOptions raccoglie le opzioni per-share applicabili a Samba.
type SMBOptions struct {
	MinProtocol     string `json:"minProtocol"`
	Signing         bool   `json:"signing"`
	Encryption      bool   `json:"encryption"`
	TimeMachine     bool   `json:"timeMachine"`
	Guest           bool   `json:"guest"`
	Oplocks         string `json:"oplocks"`
	HideDotFiles    bool   `json:"hideDotFiles"`
	CaseSensitivity string `json:"caseSensitivity"`
	Comment         string `json:"comment"`
}

// NFSRule è una regola client per una export NFS, con opzioni avanzate.
type NFSRule struct {
	Client string `json:"client"`
	Perm   string `json:"perm"`
	Adv    NFSAdv `json:"adv"`
}

type NFSAdv struct {
	Sync           bool `json:"sync"`
	NoRootSquash   bool `json:"noRootSquash"`
	RootSquash     bool `json:"rootSquash"`
	AllSquash      bool `json:"allSquash"`
	Anonuid        int  `json:"anonuid"`
	Anongid        int  `json:"anongid"`
	NoSubtreeCheck bool `json:"noSubtreeCheck"`
	Secure         bool `json:"secure"`
	Crossmnt       bool `json:"crossmnt"`
}

type NFSVersions struct {
	V3  bool `json:"v3"`
	V4  bool `json:"v4"`
	V41 bool `json:"v41"`
	V42 bool `json:"v42"`
}

// parseConfig decodifica il blob Config; ritorna una struct vuota se assente o
// malformato (retrocompatibile con le share salvate prima di questa feature).
func (s Share) parseConfig() ShareConfig {
	var c ShareConfig
	if len(s.Config) > 0 {
		_ = json.Unmarshal(s.Config, &c)
	}
	return c
}

// nfsRuleOpts costruisce la stringa di opzioni exports per una regola NFS.
func nfsRuleOpts(r NFSRule) string {
	o := make([]string, 0, 8)
	if r.Perm == "ro" {
		o = append(o, "ro")
	} else {
		o = append(o, "rw")
	}
	if r.Adv.Sync {
		o = append(o, "sync")
	} else {
		o = append(o, "async")
	}
	if r.Adv.NoSubtreeCheck {
		o = append(o, "no_subtree_check")
	} else {
		o = append(o, "subtree_check")
	}
	if r.Adv.Secure {
		o = append(o, "secure")
	} else {
		o = append(o, "insecure")
	}
	switch {
	case r.Adv.AllSquash:
		o = append(o, "all_squash")
	case r.Adv.NoRootSquash:
		o = append(o, "no_root_squash")
	default:
		o = append(o, "root_squash")
	}
	if r.Adv.Crossmnt {
		o = append(o, "crossmnt")
	}
	if r.Adv.Anonuid > 0 {
		o = append(o, fmt.Sprintf("anonuid=%d", r.Adv.Anonuid))
	}
	if r.Adv.Anongid > 0 {
		o = append(o, fmt.Sprintf("anongid=%d", r.Adv.Anongid))
	}
	return strings.Join(o, ",")
}

// confValue rende sicuro un valore testuale destinato a smb.conf: niente
// newline (che romperebbero la sezione) o caratteri di controllo.
func confValue(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
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
		// Il formato exports supporta percorsi tra virgolette (gestisce spazi).
		// Il controllo precedente ha già escluso newline e doppi apici.
		b.WriteString(fmt.Sprintf("%q", s.Path))
		cfg := s.parseConfig()
		if len(cfg.Rules) > 0 {
			// Regole per-client con opzioni avanzate (rw/ro, squash, sync, ...).
			for _, r := range cfg.Rules {
				client := strings.TrimSpace(r.Client)
				if client == "" {
					client = "*"
				}
				if strings.ContainsAny(client, " \t\r\n\"()") {
					return fmt.Errorf("client NFS non valido: %q", client)
				}
				b.WriteString(fmt.Sprintf(" %s(%s)", client, nfsRuleOpts(r)))
			}
		} else {
			// Retrocompat: usa AllowedIPs + ReadOnly.
			opts := "rw"
			if s.ReadOnly {
				opts = "ro"
			}
			hosts := s.AllowedIPs
			if len(hosts) == 0 {
				hosts = []string{"*"}
			}
			for _, h := range hosts {
				if strings.ContainsAny(h, " \t\r\n\"()") {
					return fmt.Errorf("client NFS non valido: %q", h)
				}
				b.WriteString(fmt.Sprintf(" %s(%s,sync,no_subtree_check)", h, opts))
			}
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
		// Opzioni avanzate SMB (per-share) dalla config della UI.
		cfg := s.parseConfig()
		if v := strings.TrimSpace(cfg.SMB.Comment); v != "" {
			b.WriteString(fmt.Sprintf("   comment = %s\n", confValue(v)))
		}
		if cfg.SMB.HideDotFiles {
			b.WriteString("   hide dot files = yes\n")
		}
		switch cfg.SMB.CaseSensitivity {
		case "sensitive":
			b.WriteString("   case sensitive = yes\n")
		case "insensitive":
			b.WriteString("   case sensitive = no\n")
		}
		if cfg.SMB.Oplocks == "disabled" {
			b.WriteString("   oplocks = no\n")
		}
		if cfg.SMB.Encryption {
			// Cifratura del trasporto SMB3 richiesta per questa share.
			b.WriteString("   smb encrypt = required\n")
		}
		// vfs objects: recycle (cestino) e fruit (Time Machine / macOS).
		var vfs []string
		if cfg.RecycleBin {
			vfs = append(vfs, "recycle")
		}
		if cfg.SMB.TimeMachine {
			vfs = append(vfs, "fruit")
		}
		if len(vfs) > 0 {
			b.WriteString("   vfs objects = " + strings.Join(vfs, " ") + "\n")
			if cfg.RecycleBin {
				b.WriteString("   recycle:repository = .recycle\n")
				b.WriteString("   recycle:keeptree = yes\n")
				b.WriteString("   recycle:versions = yes\n")
			}
			if cfg.SMB.TimeMachine {
				b.WriteString("   fruit:time machine = yes\n")
			}
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
