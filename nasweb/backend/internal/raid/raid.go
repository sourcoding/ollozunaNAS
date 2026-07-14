// Package raid orchestra gli array RAID software tramite mdadm e legge lo
// stato S.M.A.R.T. dei dischi tramite smartctl. Tutte le operazioni distruttive
// validano gli input e vanno confermate a livello di API.
package raid

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nasweb/nasd/internal/system"
)

// Level rappresenta un livello RAID supportato da mdadm.
type Level string

const (
	RAID0     Level = "0"
	RAID1     Level = "1"
	RAID5     Level = "5"
	RAID6     Level = "6"
	RAID10    Level = "10"
	Linear    Level = "linear" // JBOD concatenato
)

var validLevels = map[Level]int{ // livello -> n. minimo dischi
	RAID0: 2, RAID1: 2, RAID5: 3, RAID6: 4, RAID10: 4, Linear: 2,
}

// Array descrive lo stato di un array RAID.
type Array struct {
	Device    string   `json:"device"`     // /dev/md0
	Level     string   `json:"level"`
	State     string   `json:"state"`      // clean, active, degraded, rebuilding...
	NumDevices int     `json:"num_devices"`
	Devices   []string `json:"devices"`
	SyncPct   float64  `json:"sync_pct"`   // % ricostruzione/resync se in corso
}

// FilesystemInfo descrive lo stato del filesystem su un device MD.
type FilesystemInfo struct {
	Device     string `json:"device"`
	Level      string `json:"level"`
	State      string `json:"state"`
	FSType     string `json:"fstype"`      // "" = nessun filesystem
	MountPoint string `json:"mount_point"` // "" = non montato
	TotalBytes int64  `json:"total_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
	IsMounted  bool   `json:"is_mounted"`
}

// DiskInfo descrive un disco fisico rilevato da lsblk.
type DiskInfo struct {
	Device     string `json:"device"`
	SizeBytes  int64  `json:"size_bytes"`
	Model      string `json:"model"`
	Rotational bool   `json:"rotational"` // true=HDD, false=SSD
}

// Disk descrive un disco fisico e il suo stato S.M.A.R.T. sintetico.
type Disk struct {
	Device             string `json:"device"`       // /dev/sda
	Model              string `json:"model"`
	SizeBytes          int64  `json:"size_bytes"`
	SmartHealth        string `json:"smart_health"` // PASSED / FAILED / UNKNOWN
	Temperature        int    `json:"temperature"`  // °C, 0 se non disponibile
	PowerOnHours       int    `json:"power_on_hours"`
	ReallocatedSectors int    `json:"reallocated_sectors"`
}

// Manager espone le operazioni RAID.
type Manager struct {
	run system.Runner
}

func NewManager(run system.Runner) *Manager { return &Manager{run: run} }

// ListDisks elenca i dischi fisici presenti nel sistema tramite lsblk.
// osDisk restituisce il nome (es. "sde") del disco che ospita la root "/",
// così da escluderlo dalla gestione RAID. "" se non determinabile.
func (m *Manager) osDisk(ctx context.Context) string {
	src, _, err := m.run.Run(ctx, "findmnt", "-n", "-o", "SOURCE", "/")
	if err != nil {
		return ""
	}
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	// Disco padre della partizione root (PKNAME). Se vuoto, src è già un disco.
	if pk, _, e := m.run.Run(ctx, "lsblk", "-n", "-o", "PKNAME", src); e == nil {
		if name := strings.TrimSpace(strings.SplitN(pk, "\n", 2)[0]); name != "" {
			return name
		}
	}
	return strings.TrimPrefix(src, "/dev/")
}

func (m *Manager) ListDisks(ctx context.Context) ([]DiskInfo, error) {
	out, _, err := m.run.Run(ctx, "lsblk", "-b", "-d", "-n", "-o", "NAME,SIZE,ROTA,MODEL")
	if err != nil {
		return make([]DiskInfo, 0), nil
	}
	osDisk := m.osDisk(ctx) // disco che ospita "/": va nascosto dalla gestione RAID
	disks := make([]DiskInfo, 0)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		name := fields[0]
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "sr") || strings.HasPrefix(name, "md") {
			continue
		}
		if osDisk != "" && name == osDisk {
			continue // non mostrare il disco di sistema (evita RAID/wipe accidentali)
		}
		d := DiskInfo{Device: "/dev/" + name}
		if len(fields) > 1 {
			fmt.Sscanf(fields[1], "%d", &d.SizeBytes)
		}
		if len(fields) > 2 {
			d.Rotational = fields[2] == "1"
		}
		if len(fields) > 3 {
			d.Model = strings.TrimSpace(strings.Join(fields[3:], " "))
		}
		disks = append(disks, d)
	}
	return disks, nil
}

// Create crea un nuovo array. devices devono essere percorsi /dev validi.
func (m *Manager) Create(ctx context.Context, mdDevice string, level Level, devices []string) error {
	if !system.ValidMDDevice(mdDevice) {
		return fmt.Errorf("device md non valido: %s", mdDevice)
	}
	minDisks, ok := validLevels[level]
	if !ok {
		return fmt.Errorf("livello RAID non supportato: %s", level)
	}
	if len(devices) < minDisks {
		return fmt.Errorf("RAID %s richiede almeno %d dischi, forniti %d", level, minDisks, len(devices))
	}
	for _, d := range devices {
		if !system.ValidDevice(d) {
			return fmt.Errorf("device non valido: %s", d)
		}
	}

	args := []string{"--create", mdDevice, "--level=" + string(level),
		fmt.Sprintf("--raid-devices=%d", len(devices))}
	args = append(args, devices...)
	args = append(args, "--run") // evita prompt interattivo

	if _, _, err := m.run.Run(ctx, "mdadm", args...); err != nil {
		return err
	}
	// Ripulisci firme di filesystem stale: un array ricreato sugli stessi dischi
	// può esporre un vecchio superblock (es. ext4) non valido, che poi impedisce
	// il mount ("corrupted filesystem"). wipefs azzera le firme sul nuovo device.
	m.run.Run(ctx, "wipefs", "-a", mdDevice) //nolint:errcheck
	// Rendi l'array persistente: registralo in mdadm.conf e aggiorna l'initramfs,
	// altrimenti al riavvio NON viene riassemblato (il device /dev/mdN manca e i
	// mount in fstab falliscono).
	m.persistArray(ctx, mdDevice)
	return nil
}

// WipeDisk azzera un disco fisico: rimuove le firme di filesystem/RAID e la
// tabella delle partizioni (wipefs -a), riportandolo pulito e riutilizzabile.
// Rifiuta se il disco (o una sua partizione) è montato o è membro di un array.
func (m *Manager) WipeDisk(ctx context.Context, device string) error {
	if !system.ValidDevice(device) {
		return fmt.Errorf("invalid device: %s", device)
	}
	// Rifiuta solo se il disco (o una sua partizione) è montato direttamente.
	if m.isMounted(ctx, device) {
		return fmt.Errorf("disk is mounted; unmount it first")
	}
	// Se il disco è ancora membro di array md — anche assemblati automaticamente
	// e non mostrati nella UI (es. md127) — fermali e togli la config prima di
	// azzerarli, così un disco con configurazione RAID residua viene ripulito.
	// Rifiuta solo se l'array è montato (dati in uso).
	for _, md := range m.arraysForDisk(ctx, device) {
		if mp := m.findMountPoint(ctx, md); mp != "" {
			return fmt.Errorf("disk belongs to a RAID array mounted at %s; unmount it first", mp)
		}
		m.run.Run(ctx, "mdadm", "--stop", md) //nolint:errcheck
		m.mdadmConfRemove(md)
	}
	// Azzera eventuali superblock RAID residui, poi tutte le firme, infine rilegge
	// la tabella delle partizioni.
	m.run.Run(ctx, "mdadm", "--zero-superblock", device) //nolint:errcheck
	if _, _, err := m.run.Run(ctx, "wipefs", "-a", device); err != nil {
		return fmt.Errorf("wipefs failed: %w", err)
	}
	m.run.Run(ctx, "partprobe", device) //nolint:errcheck
	return nil
}

// isMounted indica se il device o una sua partizione risulta montato.
func (m *Manager) isMounted(ctx context.Context, device string) bool {
	out, _, err := m.run.Run(ctx, "cat", "/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 1 && (f[0] == device || strings.HasPrefix(f[0], device)) {
			return true
		}
	}
	return false
}

// arraysForDisk restituisce gli array md di cui il disco (o una sua partizione)
// è membro, leggendo /proc/mdstat.
func (m *Manager) arraysForDisk(ctx context.Context, device string) []string {
	base := strings.TrimPrefix(device, "/dev/")
	out, _, err := m.run.Run(ctx, "cat", "/proc/mdstat")
	if err != nil {
		return nil
	}
	var arrays []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "md") {
			continue
		}
		name := strings.SplitN(line, " ", 2)[0]
		for _, f := range strings.Fields(line) {
			if i := strings.IndexByte(f, '['); i > 0 { // token tipo "sdb[1]" o "sdb1[1]"
				if mb := f[:i]; mb == base || strings.HasPrefix(mb, base) {
					arrays = append(arrays, "/dev/"+name)
					break
				}
			}
		}
	}
	return arrays
}

// WipeFilesystem elimina il filesystem da un array: smonta se montato, lo toglie
// da fstab e cancella tutte le firme con wipefs. Serve a rimuovere il filesystem
// anche quando non è montato (o quando una firma stale ne impedisce il mount).
func (m *Manager) WipeFilesystem(ctx context.Context, mdDevice string) error {
	if !system.ValidMDDevice(mdDevice) {
		return fmt.Errorf("device md non valido: %s", mdDevice)
	}
	// Individua il mount point: da /proc/mounts se montato, altrimenti da fstab,
	// come fallback la cartella di default usata dalla UI (/srv/nas/<dev>).
	mp := m.findMountPoint(ctx, mdDevice)
	if mp != "" {
		if _, _, err := m.run.Run(ctx, "umount", mdDevice); err != nil {
			return fmt.Errorf("umount fallito: %w", err)
		}
	} else if fm := m.fstabMountPoint(mdDevice); fm != "" {
		mp = fm
	} else {
		mp = "/srv/nas/" + strings.TrimPrefix(mdDevice, "/dev/")
	}
	if mp != "" {
		m.fstabRemove(mp)
	}
	if _, _, err := m.run.Run(ctx, "wipefs", "-a", mdDevice); err != nil {
		return fmt.Errorf("wipefs fallito: %w", err)
	}
	// Rimuove la cartella del mount point se ora è vuota (rmdir non tocca dir piene).
	if strings.HasPrefix(mp, "/") && mp != "/" {
		m.run.Run(ctx, "rmdir", mp) //nolint:errcheck
	}
	return nil
}

// fstabMountPoint restituisce il mount point associato a device in /etc/fstab.
func (m *Manager) fstabMountPoint(device string) string {
	data, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == device {
			return f[1]
		}
	}
	return ""
}

// persistArray aggiunge la riga ARRAY di mdDevice a /etc/mdadm/mdadm.conf (se
// non già presente, deduplicando per UUID) e rigenera l'initramfs.
func (m *Manager) persistArray(ctx context.Context, mdDevice string) {
	out, _, err := m.run.Run(ctx, "mdadm", "--detail", "--scan", mdDevice)
	if err != nil {
		return
	}
	line := strings.TrimSpace(out)
	if !strings.HasPrefix(line, "ARRAY") {
		return
	}
	const conf = "/etc/mdadm/mdadm.conf"
	data, _ := os.ReadFile(conf)
	// Dedup per UUID quando disponibile, altrimenti per device.
	key := mdDevice
	for _, tok := range strings.Fields(line) {
		if strings.HasPrefix(tok, "UUID=") {
			key = tok
			break
		}
	}
	if strings.Contains(string(data), key) {
		return
	}
	f, err := os.OpenFile(conf, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintln(f, line)
	f.Close()
	m.run.Run(ctx, "update-initramfs", "-u") //nolint:errcheck
}

// Stop ferma un array (operazione distruttiva: richiede conferma a livello API).
// Smonta automaticamente il filesystem se montato.
func (m *Manager) Stop(ctx context.Context, mdDevice string) error {
	if !system.ValidMDDevice(mdDevice) {
		return fmt.Errorf("device md non valido: %s", mdDevice)
	}
	if mp := m.findMountPoint(ctx, mdDevice); mp != "" {
		m.run.Run(ctx, "umount", mp) //nolint:errcheck
		m.fstabRemove(mp)
	}
	// Rileva i dischi membri PRIMA dello stop: dopo `mdadm --stop` l'array non è
	// più interrogabile e i membri non sarebbero più ricavabili.
	members := m.arrayMembers(ctx, mdDevice)
	m.mdadmConfRemove(mdDevice)
	if _, _, err := m.run.Run(ctx, "mdadm", "--stop", mdDevice); err != nil {
		return err
	}
	// Cancellare l'array NON basta: il superblock RAID resta scritto su ogni
	// disco membro, quindi mdadm lo riassembla al boot (tipicamente come
	// /dev/md127). Azzeriamo il superblock e cancelliamo tutte le firme residue
	// (wipefs) su ogni membro, così la config non "riappare".
	for _, d := range members {
		m.run.Run(ctx, "mdadm", "--zero-superblock", d) //nolint:errcheck
		m.run.Run(ctx, "wipefs", "-a", d)               //nolint:errcheck
		m.run.Run(ctx, "partprobe", d)                  //nolint:errcheck
	}
	return nil
}

// arrayMembers restituisce i device membri (dischi o partizioni) di un array md,
// letti da `mdadm --detail`. Va chiamato PRIMA dello stop dell'array.
func (m *Manager) arrayMembers(ctx context.Context, mdDevice string) []string {
	out, _, err := m.run.Run(ctx, "mdadm", "--detail", mdDevice)
	if err != nil {
		return nil
	}
	var devs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		last := f[len(f)-1]
		// Le righe della tabella membri terminano con il path del device
		// (/dev/sdb, /dev/sda1, ...). Escludiamo il device md stesso.
		if strings.HasPrefix(last, "/dev/") && last != mdDevice && !seen[last] {
			seen[last] = true
			devs = append(devs, last)
		}
	}
	return devs
}

// mdadmConfRemove elimina le righe ARRAY di mdDevice da mdadm.conf, così un
// array cancellato non resta referenziato all'avvio.
func (m *Manager) mdadmConfRemove(mdDevice string) {
	const conf = "/etc/mdadm/mdadm.conf"
	data, err := os.ReadFile(conf)
	if err != nil {
		return
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "ARRAY") && strings.Contains(line, mdDevice) {
			continue
		}
		kept = append(kept, line)
	}
	_ = os.WriteFile(conf, []byte(strings.Join(kept, "\n")), 0o644)
}

// AddDisk aggiunge un disco a un array (es. hot-spare o sostituzione).
func (m *Manager) AddDisk(ctx context.Context, mdDevice, disk string) error {
	if !system.ValidMDDevice(mdDevice) || !system.ValidDevice(disk) {
		return fmt.Errorf("parametri non validi")
	}
	_, _, err := m.run.Run(ctx, "mdadm", "--add", mdDevice, disk)
	return err
}

// RemoveDisk marca un disco come failed e lo rimuove dall'array.
func (m *Manager) RemoveDisk(ctx context.Context, mdDevice, disk string) error {
	if !system.ValidMDDevice(mdDevice) || !system.ValidDevice(disk) {
		return fmt.Errorf("parametri non validi")
	}
	if _, _, err := m.run.Run(ctx, "mdadm", "--fail", mdDevice, disk); err != nil {
		return err
	}
	_, _, err := m.run.Run(ctx, "mdadm", "--remove", mdDevice, disk)
	return err
}

// List legge /proc/mdstat e arricchisce con `mdadm --detail` per ogni array.
func (m *Manager) List(ctx context.Context) ([]Array, error) {
	out, _, err := m.run.Run(ctx, "cat", "/proc/mdstat")
	if err != nil {
		return nil, err
	}
	arrays := make([]Array, 0)
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "md") {
			continue
		}
		name := strings.SplitN(line, " ", 2)[0]
		dev := "/dev/" + name
		a, derr := m.Detail(ctx, dev)
		if derr == nil {
			arrays = append(arrays, *a)
		}
	}
	return arrays, nil
}

// Detail esegue `mdadm --detail` e ne estrae i campi principali.
func (m *Manager) Detail(ctx context.Context, mdDevice string) (*Array, error) {
	out, _, err := m.run.Run(ctx, "mdadm", "--detail", mdDevice)
	if err != nil {
		return nil, err
	}
	a := &Array{Device: mdDevice, Devices: make([]string, 0)}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			switch k {
			case "Raid Level":
				a.Level = strings.TrimPrefix(v, "raid")
			case "State":
				a.State = v
			case "Raid Devices":
				fmt.Sscanf(v, "%d", &a.NumDevices)
			}
		}
		// device member lines: "  0  8  16  0  active sync  /dev/sdb"
		fields := strings.Fields(line)
		if len(fields) >= 6 && strings.HasPrefix(fields[len(fields)-1], "/dev/") {
			a.Devices = append(a.Devices, fields[len(fields)-1])
		}
	}
	return a, nil
}

// validFS mappa i tipi filesystem ai loro comandi mkfs.
var validFS = map[string]struct{ cmd, flag string }{
	"ext4":  {"mkfs.ext4", "-F"},
	"ext3":  {"mkfs.ext3", "-F"},
	"btrfs": {"mkfs.btrfs", "-f"},
	"xfs":   {"mkfs.xfs", "-f"},
}

// mkfsPkg mappa il tipo filesystem al pacchetto Debian che fornisce il suo mkfs.
var mkfsPkg = map[string]string{
	"ext4":  "e2fsprogs",
	"ext3":  "e2fsprogs",
	"btrfs": "btrfs-progs",
	"xfs":   "xfsprogs",
}

// ensureMkfs garantisce che il comando mkfs richiesto sia disponibile; se manca
// (es. btrfs-progs non preinstallato), installa il pacchetto corrispondente via
// apt in un'unità transitoria — FUORI dalla sandbox di nasd, altrimenti
// ProtectSystem=true renderebbe /usr read-only a dpkg. Idempotente: se il
// comando c'è già, non fa nulla. Errore chiaro se l'installazione fallisce.
func (m *Manager) ensureMkfs(ctx context.Context, cmd, fsType string) error {
	has := func() bool {
		_, _, err := m.run.Run(ctx, "sh", "-c", "command -v "+cmd+" >/dev/null 2>&1")
		return err == nil
	}
	if has() {
		return nil
	}
	pkg := mkfsPkg[fsType]
	if pkg == "" {
		return fmt.Errorf("%s non trovato e nessun pacchetto noto per il filesystem %s", cmd, fsType)
	}
	apt := func(script string) {
		m.run.Run(ctx, "systemd-run", "--wait", "--collect", "--pipe", "--quiet", //nolint:errcheck
			"--", "sh", "-c", "DEBIAN_FRONTEND=noninteractive "+script+" 2>&1")
	}
	apt("apt-get install -y --no-install-recommends " + pkg)
	if !has() {
		apt("apt-get update -y; apt-get install -y --no-install-recommends " + pkg)
	}
	if !has() {
		return fmt.Errorf("%s non disponibile: installazione del pacchetto %s fallita (serve connessione di rete)", cmd, pkg)
	}
	return nil
}

// Format crea un filesystem sul device md. DISTRUTTIVO: cancella tutti i dati.
func (m *Manager) Format(ctx context.Context, mdDevice, fsType string) error {
	if !system.ValidMDDevice(mdDevice) {
		return fmt.Errorf("device md non valido: %s", mdDevice)
	}
	mc, ok := validFS[fsType]
	if !ok {
		return fmt.Errorf("filesystem non supportato: %s", fsType)
	}
	if err := m.ensureMkfs(ctx, mc.cmd, fsType); err != nil {
		return err
	}
	_, _, err := m.run.Run(ctx, mc.cmd, mc.flag, mdDevice)
	return err
}

// ListFilesystems restituisce info filesystem per tutti gli array MD.
func (m *Manager) ListFilesystems(ctx context.Context) ([]FilesystemInfo, error) {
	arrays, err := m.List(ctx)
	if err != nil {
		return make([]FilesystemInfo, 0), nil
	}
	type mi struct{ mountPoint, fsType string }
	mounted := make(map[string]mi)
	if out, _, e := m.run.Run(ctx, "cat", "/proc/mounts"); e == nil {
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) >= 3 && strings.HasPrefix(f[0], "/dev/md") {
				mounted[f[0]] = mi{f[1], f[2]}
			}
		}
	}
	result := make([]FilesystemInfo, 0, len(arrays))
	for _, a := range arrays {
		fi := FilesystemInfo{Device: a.Device, Level: a.Level, State: a.State}
		if m2, ok := mounted[a.Device]; ok {
			fi.MountPoint = m2.mountPoint
			fi.FSType = m2.fsType
			fi.IsMounted = true
			fi.TotalBytes, fi.UsedBytes, fi.FreeBytes = m.dfUsage(ctx, m2.mountPoint)
		} else {
			if out, _, e := m.run.Run(ctx, "blkid", "-s", "TYPE", "-o", "value", a.Device); e == nil {
				fi.FSType = strings.TrimSpace(out)
			}
		}
		result = append(result, fi)
	}
	return result, nil
}

func (m *Manager) dfUsage(ctx context.Context, mountPoint string) (total, used, free int64) {
	out, _, err := m.run.Run(ctx, "df", "-B1", mountPoint)
	if err != nil {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && strings.HasPrefix(f[0], "/dev/") {
			fmt.Sscanf(f[1], "%d", &total)
			fmt.Sscanf(f[2], "%d", &used)
			fmt.Sscanf(f[3], "%d", &free)
			return
		}
	}
	return
}

func (m *Manager) findMountPoint(ctx context.Context, mdDevice string) string {
	out, _, err := m.run.Run(ctx, "cat", "/proc/mounts")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == mdDevice {
			return f[1]
		}
	}
	return ""
}

// CreateFilesystem formatta un array MD e lo monta sul mountPoint dato.
func (m *Manager) CreateFilesystem(ctx context.Context, mdDevice, fsType, mountPoint string) error {
	if !system.ValidMDDevice(mdDevice) {
		return fmt.Errorf("device md non valido: %s", mdDevice)
	}
	mc, ok := validFS[fsType]
	if !ok {
		return fmt.Errorf("filesystem non supportato: %s", fsType)
	}
	if !strings.HasPrefix(mountPoint, "/") || strings.ContainsAny(mountPoint, "\n\r\x00\"'`") {
		return fmt.Errorf("mount point non valido: %s", mountPoint)
	}
	if err := m.ensureMkfs(ctx, mc.cmd, fsType); err != nil {
		return err
	}
	if _, _, err := m.run.Run(ctx, mc.cmd, mc.flag, mdDevice); err != nil {
		return fmt.Errorf("mkfs fallito: %w", err)
	}
	if _, _, err := m.run.Run(ctx, "mkdir", "-p", mountPoint); err != nil {
		return fmt.Errorf("mkdir fallito: %w", err)
	}
	if _, _, err := m.run.Run(ctx, "mount", "-t", fsType, mdDevice, mountPoint); err != nil {
		return fmt.Errorf("mount fallito: %w", err)
	}
	m.fstabAdd(mdDevice, mountPoint, fsType)
	return nil
}

// UnmountFilesystem smonta il filesystem e lo rimuove da fstab.
func (m *Manager) UnmountFilesystem(ctx context.Context, mountPoint string) error {
	if !strings.HasPrefix(mountPoint, "/") || strings.ContainsAny(mountPoint, "\n\r\x00\"'`") {
		return fmt.Errorf("mount point non valido")
	}
	if _, _, err := m.run.Run(ctx, "umount", mountPoint); err != nil {
		return fmt.Errorf("umount fallito: %w", err)
	}
	m.fstabRemove(mountPoint)
	return nil
}

// MountOnly monta un filesystem già formattato senza riformattare.
func (m *Manager) MountOnly(ctx context.Context, mdDevice, fsType, mountPoint string) error {
	if !system.ValidMDDevice(mdDevice) {
		return fmt.Errorf("device md non valido: %s", mdDevice)
	}
	if !strings.HasPrefix(mountPoint, "/") || strings.ContainsAny(mountPoint, "\n\r\x00\"'`") {
		return fmt.Errorf("mount point non valido: %s", mountPoint)
	}
	if _, _, err := m.run.Run(ctx, "mkdir", "-p", mountPoint); err != nil {
		return fmt.Errorf("mkdir fallito: %w", err)
	}
	args := []string{mdDevice, mountPoint}
	if fsType != "" {
		args = []string{"-t", fsType, mdDevice, mountPoint}
	}
	if _, _, err := m.run.Run(ctx, "mount", args...); err != nil {
		return fmt.Errorf("mount fallito: %w", err)
	}
	m.fstabAdd(mdDevice, mountPoint, fsType)
	return nil
}

// GrowFilesystem espande il filesystem dopo che l'array è stato allargato.
func (m *Manager) GrowFilesystem(ctx context.Context, mdDevice, fsType string) error {
	if !system.ValidMDDevice(mdDevice) {
		return fmt.Errorf("device md non valido: %s", mdDevice)
	}
	switch fsType {
	case "ext4", "ext3":
		_, _, err := m.run.Run(ctx, "resize2fs", mdDevice)
		return err
	case "xfs":
		mp := m.findMountPoint(ctx, mdDevice)
		if mp == "" {
			return fmt.Errorf("xfs: dispositivo non montato")
		}
		_, _, err := m.run.Run(ctx, "xfs_growfs", mp)
		return err
	case "btrfs":
		mp := m.findMountPoint(ctx, mdDevice)
		if mp == "" {
			return fmt.Errorf("btrfs: dispositivo non montato")
		}
		_, _, err := m.run.Run(ctx, "btrfs", "filesystem", "resize", "max", mp)
		return err
	default:
		return fmt.Errorf("grow non supportato per filesystem: %s", fsType)
	}
}

func (m *Manager) fstabAdd(device, mountPoint, fsType string) {
	// Sorgente per UUID del filesystem, NON per /dev/mdN: al boot mdadm può
	// riassemblare l'array con un nome diverso da quello di creazione (tipicamente
	// /dev/md127 invece di /dev/md0), e una riga fstab che punta a /dev/md0 non
	// troverebbe il device → con nofail il volume resta smontato. L'UUID del
	// filesystem è stabile a prescindere dal nome dell'array.
	src := device
	if out, _, err := m.run.Run(context.Background(), "blkid", "-s", "UUID", "-o", "value", device); err == nil {
		if uuid := strings.TrimSpace(out); uuid != "" {
			src = "UUID=" + uuid
		}
	}
	// nofail + timeout breve: se l'array non è (ancora) assemblato al boot, il
	// sistema NON deve cadere in emergency mode (local-fs.target). Senza nofail
	// un array mancante blocca l'avvio dell'intera macchina.
	entry := src + "\t" + mountPoint + "\t" + fsType + "\tnofail,x-systemd.device-timeout=10s\t0\t0"
	data, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return
		}
	}
	f, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, entry)
}

func (m *Manager) fstabRemove(mountPoint string) {
	data, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == mountPoint {
			continue
		}
		kept = append(kept, line)
	}
	_ = os.WriteFile("/etc/fstab", []byte(strings.Join(kept, "\n")), 0644)
}

// SmartInfo legge lo stato S.M.A.R.T. di un disco via smartctl.
func (m *Manager) SmartInfo(ctx context.Context, disk string) (*Disk, error) {
	if !system.ValidDevice(disk) {
		return nil, fmt.Errorf("device non valido")
	}
	out, _, err := m.run.Run(ctx, "smartctl", "-a", disk)
	if err != nil && out == "" {
		return nil, err
	}
	d := &Disk{Device: disk, SmartHealth: "UNKNOWN"}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "Device Model"), strings.Contains(line, "Model Number"):
			if _, v, ok := strings.Cut(line, ":"); ok {
				d.Model = strings.TrimSpace(v)
			}
		case strings.Contains(line, "SMART overall-health"):
			if strings.Contains(line, "PASSED") {
				d.SmartHealth = "PASSED"
			} else if strings.Contains(line, "FAILED") {
				d.SmartHealth = "FAILED"
			}
		case strings.Contains(line, "Temperature_Celsius"):
			fields := strings.Fields(line)
			if len(fields) >= 10 {
				fmt.Sscanf(fields[9], "%d", &d.Temperature)
			}
		case strings.Contains(line, "Current Drive Temperature"):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				fmt.Sscanf(fields[len(fields)-2], "%d", &d.Temperature)
			}
		case strings.Contains(line, "Power_On_Hours"):
			fields := strings.Fields(line)
			if len(fields) >= 10 {
				fmt.Sscanf(fields[9], "%d", &d.PowerOnHours)
			}
		case strings.Contains(line, "Reallocated_Sector_Ct"):
			fields := strings.Fields(line)
			if len(fields) >= 10 {
				fmt.Sscanf(fields[9], "%d", &d.ReallocatedSectors)
			}
		}
	}
	return d, nil
}
