package qbt

import (
	"context"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// Identità del servizio e percorsi (config nella home del servizio, MAI sui volumi).
const (
	SvcUser  = "qbtuser"
	SvcGroup = "nas-media"
	HomeDir  = "/var/lib/qbt"
	UnitPath = "/etc/systemd/system/" + ServiceUnit
)

// Config è l'input del wizard.
type Config struct {
	TempDir      string `json:"temp_dir"`
	TorfileDir   string `json:"torfile_dir"`
	DownloadsDir string `json:"downloads_dir"`
	PermsMode    string `json:"perms_mode"` // "2770" (privato) o "2775" (leggibile da altre app/share)
	WebUIPort    int    `json:"webui_port"`
}

// Step è l'esito di un passo di applicazione (per il feedback nel wizard).
type Step struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Msg  string `json:"msg"`
}

// Entry è una voce del file browser ristretto ai volumi.
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// ---- Validazione path dentro i volumi -------------------------------------

// resolveInVolumes verifica che path, canonicalizzato (symlink risolti sul
// prefisso esistente), stia dentro un volume dati. Blocca traversal e symlink
// che escono dai volumi consentiti.
func (m *Manager) resolveInVolumes(ctx context.Context, path string) (*Volume, error) {
	if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) {
		return nil, fmt.Errorf("invalid path")
	}
	clean := filepath.Clean(path)
	real := canonExisting(clean)
	for _, v := range m.vols(ctx) {
		mp := filepath.Clean(v.MountPoint)
		if real == mp || strings.HasPrefix(real+string(os.PathSeparator), mp+string(os.PathSeparator)) {
			vv := v
			return &vv, nil
		}
	}
	return nil, fmt.Errorf("path is outside data volumes")
}

// canonExisting risolve i symlink sul prefisso esistente del path, riattaccando
// il suffisso non ancora creato: così un symlink nel prefisso che punta fuori
// dai volumi viene intercettato.
func canonExisting(path string) string {
	p := path
	var suffix []string
	for {
		if _, err := os.Lstat(p); err == nil {
			if real, err := filepath.EvalSymlinks(p); err == nil {
				p = real
			}
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		suffix = append([]string{filepath.Base(p)}, suffix...)
		p = parent
	}
	if len(suffix) > 0 {
		return filepath.Join(append([]string{p}, suffix...)...)
	}
	return p
}

// ---- Browse / Mkdir / Validate --------------------------------------------

// Browse elenca le sottodirectory dentro un volume. Con path vuoto elenca i
// volumi come radici (mai la rootfs di sistema).
func (m *Manager) Browse(ctx context.Context, path string) ([]Entry, error) {
	out := make([]Entry, 0)
	if path == "" {
		for _, v := range m.vols(ctx) {
			out = append(out, Entry{Name: v.Name, Path: v.MountPoint, IsDir: true})
		}
		return out, nil
	}
	if _, err := m.resolveInVolumes(ctx, path); err != nil {
		return nil, err
	}
	items, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.IsDir() {
			out = append(out, Entry{Name: it.Name(), Path: filepath.Join(path, it.Name()), IsDir: true})
		}
	}
	return out, nil
}

func validName(name string) bool {
	if name == "" || len(name) > 255 || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\x00") || strings.Contains(name, "..") {
		return false
	}
	return true
}

// Mkdir crea una cartella dentro un volume (nome validato, no traversal, no dup).
func (m *Manager) Mkdir(ctx context.Context, parent, name string) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("invalid folder name")
	}
	if _, err := m.resolveInVolumes(ctx, parent); err != nil {
		return "", err
	}
	dst := filepath.Join(parent, name)
	if _, err := m.resolveInVolumes(ctx, dst); err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("folder already exists")
	}
	if err := os.MkdirAll(dst, 0o2775); err != nil {
		return "", err
	}
	return dst, nil
}

// ValidateResult è l'esito della validazione al riepilogo del wizard.
type ValidateResult struct {
	OK       bool             `json:"ok"`
	Errors   []string         `json:"errors"`
	Warnings []string         `json:"warnings"`
	Free     map[string]int64 `json:"free"`
}

// Validate controlla che le 3 directory siano dentro volumi, distinte e con spazio.
func (m *Manager) Validate(ctx context.Context, temp, torfile, downloads string) ValidateResult {
	res := ValidateResult{OK: true, Errors: []string{}, Warnings: []string{}, Free: map[string]int64{}}
	for key, d := range map[string]string{"temp_dir": temp, "torfile_dir": torfile, "downloads_dir": downloads} {
		if d == "" {
			res.Errors = append(res.Errors, key+": required")
			continue
		}
		v, err := m.resolveInVolumes(ctx, d)
		if err != nil {
			res.Errors = append(res.Errors, key+": "+err.Error())
			continue
		}
		res.Free[key] = v.FreeBytes
	}
	if temp != "" && temp == downloads {
		res.Errors = append(res.Errors, "temp_dir and downloads_dir must be different")
	}
	if torfile != "" && (torfile == temp || torfile == downloads) {
		res.Warnings = append(res.Warnings, "torfile_dir coincides with another directory")
	}
	res.OK = len(res.Errors) == 0
	return res
}

// ---- Setup privilegiato ----------------------------------------------------

// EnsureInstalled installa qbittorrent-nox + acl se mancanti (app installabile).
func (m *Manager) EnsureInstalled(ctx context.Context) error {
	if _, _, err := m.run.Run(ctx, "sh", "-c", "command -v qbittorrent-nox >/dev/null 2>&1"); err == nil {
		return nil
	}
	m.aptRun(ctx, "apt-get update -y")
	out, ok := m.aptRun(ctx, "apt-get install -y --no-install-recommends qbittorrent-nox acl")
	if !ok {
		// Recupero da stato dpkg incompleto (fuori sandbox), poi riprova una volta.
		m.aptRun(ctx, "dpkg --configure -a; apt-get -f install -y")
		out2, ok2 := m.aptRun(ctx, "apt-get install -y --no-install-recommends qbittorrent-nox acl")
		if ok2 {
			return nil
		}
		return fmt.Errorf("apt install: %s", lastLines(out+"\n"+out2, 8))
	}
	return nil
}

// aptRun esegue un comando apt/dpkg tramite systemd-run: gira in un'unità
// transitoria FUORI dalla sandbox di nasd (ProtectSystem=true renderebbe /usr
// read-only ai figli di nasd, facendo fallire dpkg). Restituisce l'output
// combinato e l'esito.
func (m *Manager) aptRun(ctx context.Context, script string) (string, bool) {
	out, _, err := m.run.Run(ctx, "systemd-run", "--wait", "--collect", "--pipe", "--quiet",
		"--", "sh", "-c", "DEBIAN_FRONTEND=noninteractive "+script+" 2>&1")
	return out, err == nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// EnsureUserGroup crea gruppo nas-media e utente di servizio qbtuser.
func (m *Manager) EnsureUserGroup(ctx context.Context) {
	m.run.Run(ctx, "groupadd", "-f", SvcGroup)                                             //nolint:errcheck
	if _, _, err := m.run.Run(ctx, "id", SvcUser); err != nil {
		m.run.Run(ctx, "useradd", "--system", "--home-dir", HomeDir, "--create-home", //nolint:errcheck
			"--shell", "/usr/sbin/nologin", "-g", SvcGroup, SvcUser)
	}
	m.run.Run(ctx, "usermod", "-g", SvcGroup, SvcUser)                     //nolint:errcheck
	m.run.Run(ctx, "mkdir", "-p", HomeDir+"/.config/qBittorrent")          //nolint:errcheck
	m.run.Run(ctx, "chown", "-R", SvcUser+":"+SvcGroup, HomeDir)           //nolint:errcheck
}

// applyDir crea la directory, imposta owner/permessi/setgid/ACL e verifica
// l'accesso REALE impersonando l'utente di servizio (intercetta ACL, noexec, ro).
func (m *Manager) applyDir(ctx context.Context, dir, mode string, steps *[]Step) bool {
	if _, _, err := m.run.Run(ctx, "mkdir", "-p", dir); err != nil {
		*steps = append(*steps, Step{"mkdir " + dir, false, err.Error()})
		return false
	}
	m.run.Run(ctx, "chown", SvcUser+":"+SvcGroup, dir)                       //nolint:errcheck
	m.run.Run(ctx, "chmod", mode, dir)                                       //nolint:errcheck
	m.run.Run(ctx, "setfacl", "-d", "-m", "g:"+SvcGroup+":rwx", dir)         //nolint:errcheck
	if _, _, err := m.run.Run(ctx, "runuser", "-u", SvcUser, "--", "test", "-w", dir); err != nil {
		*steps = append(*steps, Step{"verify write " + dir, false, SvcUser + " cannot write"})
		return false
	}
	*steps = append(*steps, Step{dir, true, "owner " + SvcUser + ":" + SvcGroup + " mode " + mode})
	return true
}

// webuiPassword genera l'hash PBKDF2 nel formato di qBittorrent per la WebUI.
func webuiPassword(plain string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := pbkdf2.Key([]byte(plain), salt, 100000, 64, sha512.New)
	return "@ByteArray(" + base64.StdEncoding.EncodeToString(salt) + ":" + base64.StdEncoding.EncodeToString(hash) + ")", nil
}

func randomPassword() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[n.Int64()]
	}
	return string(b)
}

// writeConfig scrive qBittorrent.conf (owner qbtuser) nella config dir del servizio.
func (m *Manager) writeConfig(ctx context.Context, cfg Config, pbkdf2Pass string) error {
	confDir := HomeDir + "/.config/qBittorrent"
	m.run.Run(ctx, "mkdir", "-p", confDir) //nolint:errcheck
	var b strings.Builder
	b.WriteString("[BitTorrent]\n")
	b.WriteString("Session\\DefaultSavePath=" + cfg.DownloadsDir + "\n")
	b.WriteString("Session\\TempPath=" + cfg.TempDir + "\n")
	b.WriteString("Session\\TempPathEnabled=true\n")
	b.WriteString("Session\\TorrentExportDirectory=" + cfg.TorfileDir + "\n")
	b.WriteString("Session\\FinishedTorrentExportDirectory=" + cfg.TorfileDir + "\n")
	// Cartella monitorata per i .torrent (formato legacy, best-effort).
	b.WriteString("Session\\ScanDirs\\1\\path=" + cfg.TorfileDir + "\n")
	b.WriteString("Session\\ScanDirs\\1\\download_path_enabled=false\n")
	b.WriteString("\n[LegalNotice]\nAccepted=true\n")
	b.WriteString("\n[Preferences]\n")
	b.WriteString("WebUI\\Port=" + strconv.Itoa(cfg.WebUIPort) + "\n")
	b.WriteString("WebUI\\Address=*\n")
	b.WriteString("WebUI\\Username=admin\n")
	b.WriteString("WebUI\\Password_PBKDF2=\"" + pbkdf2Pass + "\"\n")
	confFile := confDir + "/qBittorrent.conf"
	if err := os.WriteFile(confFile, []byte(b.String()), 0o640); err != nil {
		return err
	}
	m.run.Run(ctx, "chown", "-R", SvcUser+":"+SvcGroup, HomeDir+"/.config") //nolint:errcheck
	return nil
}

// installUnit scrive/aggiorna l'unità systemd che gira come qbtuser:nas-media.
func (m *Manager) installUnit(ctx context.Context, port int) error {
	unit := "[Unit]\n" +
		"Description=qBittorrent-nox (managed by nasd)\n" +
		"After=network-online.target\nWants=network-online.target\n\n" +
		"[Service]\nUser=" + SvcUser + "\nGroup=" + SvcGroup + "\nUMask=0002\n" +
		"ExecStart=/usr/bin/qbittorrent-nox --confirm-legal-notice --webui-port=" + strconv.Itoa(port) + "\n" +
		"Restart=on-failure\nRestartSec=3\n\n[Install]\nWantedBy=multi-user.target\n"
	if err := os.WriteFile(UnitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	m.run.Run(ctx, "systemctl", "daemon-reload") //nolint:errcheck
	return nil
}

// Start / Stop del servizio.
func (m *Manager) Start(ctx context.Context) error {
	_, stderr, err := m.run.Run(ctx, "systemctl", "enable", "--now", ServiceUnit)
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(stderr))
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	_, _, err := m.run.Run(ctx, "systemctl", "stop", ServiceUnit)
	return err
}

// Configure applica l'intera configurazione in modo idempotente. Restituisce gli
// step (per il feedback) e la password WebUI generata (mostrata una sola volta).
func (m *Manager) Configure(ctx context.Context, cfg Config) (steps []Step, webuiPass string, err error) {
	if vr := m.Validate(ctx, cfg.TempDir, cfg.TorfileDir, cfg.DownloadsDir); !vr.OK {
		return steps, "", fmt.Errorf("validation failed: %s", strings.Join(vr.Errors, "; "))
	}
	if e := m.EnsureInstalled(ctx); e != nil {
		steps = append(steps, Step{"install qbittorrent-nox", false, e.Error()})
		return steps, "", e
	}
	steps = append(steps, Step{"qbittorrent-nox installed", true, ""})

	m.EnsureUserGroup(ctx)
	steps = append(steps, Step{"service user " + SvcUser + ":" + SvcGroup, true, ""})

	mode := cfg.PermsMode
	if mode != "2775" {
		mode = "2770"
	}
	for _, d := range []string{cfg.TempDir, cfg.TorfileDir, cfg.DownloadsDir} {
		if !m.applyDir(ctx, d, mode, &steps) {
			return steps, "", fmt.Errorf("failed to prepare %s", d)
		}
	}

	m.run.Run(ctx, "systemctl", "stop", ServiceUnit) //nolint:errcheck (idempotente)
	webuiPass = randomPassword()
	pbk, e := webuiPassword(webuiPass)
	if e != nil {
		return steps, "", e
	}
	if e := m.writeConfig(ctx, cfg, pbk); e != nil {
		steps = append(steps, Step{"write qBittorrent.conf", false, e.Error()})
		return steps, "", e
	}
	steps = append(steps, Step{"qBittorrent.conf written", true, ""})

	if e := m.installUnit(ctx, cfg.WebUIPort); e != nil {
		steps = append(steps, Step{"systemd unit", false, e.Error()})
		return steps, "", e
	}
	if e := m.Start(ctx); e != nil {
		steps = append(steps, Step{"start service", false, e.Error()})
		return steps, webuiPass, e
	}
	steps = append(steps, Step{"service started (WebUI :" + strconv.Itoa(cfg.WebUIPort) + ")", true, ""})
	return steps, webuiPass, nil
}
