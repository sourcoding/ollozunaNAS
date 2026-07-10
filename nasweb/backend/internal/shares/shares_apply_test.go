package shares

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recRunner registra i comandi eseguiti e restituisce sempre successo.
type recRunner struct{ cmds [][]string }

func (r *recRunner) Run(_ context.Context, name string, args ...string) (string, string, error) {
	r.cmds = append(r.cmds, append([]string{name}, args...))
	return "", "", nil
}
func (r *recRunner) RunStdin(_ context.Context, _ string, name string, args ...string) (string, string, error) {
	r.cmds = append(r.cmds, append([]string{name}, args...))
	return "", "", nil
}
func (r *recRunner) ran(sub string) bool {
	for _, c := range r.cmds {
		if strings.Contains(strings.Join(c, " "), sub) {
			return true
		}
	}
	return false
}

func TestApplyNFSGeneratesExportsAndStartsServer(t *testing.T) {
	dir := t.TempDir()
	exports := filepath.Join(dir, "exports")
	r := &recRunner{}
	m := NewManager(r, exports, filepath.Join(dir, "smb.conf"))

	sh := []Share{
		{Name: "pub", Path: "/srv/nfspub", Protocol: NFS, AllowedIPs: []string{"192.168.0.0/24"}, Enabled: true},
		{Name: "ro", Path: "/srv/nfsro", Protocol: NFS, ReadOnly: true, Enabled: true},
		{Name: "off", Path: "/srv/x", Protocol: NFS, Enabled: false},
	}
	if err := m.ApplyNFS(context.Background(), sh); err != nil {
		t.Fatalf("ApplyNFS: %v", err)
	}
	data, _ := os.ReadFile(exports)
	got := string(data)
	for _, want := range []string{
		`"/srv/nfspub" 192.168.0.0/24(rw,sync,no_subtree_check)`,
		`"/srv/nfsro" *(ro,sync,no_subtree_check)`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("exports non contiene %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/srv/x") {
		t.Fatal("export disabilitata non deve comparire")
	}
	if !r.ran("mkdir -p /srv/nfspub") {
		t.Fatal("manca mkdir della cartella export")
	}
	if !r.ran("systemctl enable --now nfs-server") {
		t.Fatal("nfs-server non avviato con export abilitate")
	}
	if !r.ran("exportfs -ra") {
		t.Fatal("manca exportfs -ra")
	}
}

func TestApplyNFSNoExportsStopsServer(t *testing.T) {
	dir := t.TempDir()
	r := &recRunner{}
	m := NewManager(r, filepath.Join(dir, "exports"), filepath.Join(dir, "smb.conf"))
	if err := m.ApplyNFS(context.Background(), nil); err != nil {
		t.Fatalf("ApplyNFS: %v", err)
	}
	if !r.ran("systemctl disable --now nfs-server") {
		t.Fatal("senza export nfs-server dovrebbe essere fermato")
	}
}

func TestApplySMBGuestAndAuth(t *testing.T) {
	dir := t.TempDir()
	smbconf := filepath.Join(dir, "smb.conf")
	os.WriteFile(smbconf, []byte("[global]\n"), 0o644)
	r := &recRunner{}
	m := NewManager(r, filepath.Join(dir, "exports"), smbconf)

	sh := []Share{
		{Name: "pub", Path: "/srv/pub", Protocol: SMB, Enabled: true},                                         // niente utenti -> guest
		{Name: "priv", Path: "/srv/priv", Protocol: SMB, ValidUsers: []string{"alice", "@team"}, ReadOnly: true, Enabled: true},
	}
	if err := m.ApplySMB(context.Background(), sh); err != nil {
		t.Fatalf("ApplySMB: %v", err)
	}
	inc, _ := os.ReadFile(filepath.Join(dir, "nasd-shares.conf"))
	got := string(inc)
	for _, want := range []string{
		"[pub]", "path = /srv/pub", "guest ok = yes",
		"[priv]", "valid users = alice @team", "read only = true",
		"create mask = 0664", "directory mask = 0775",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("config samba non contiene %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[priv]\n   path = /srv/priv\n   read only = true\n   guest ok") {
		t.Fatal("share con utenti non deve avere guest ok")
	}
	if !r.ran("mkdir -p /srv/pub") || !r.ran("mkdir -p /srv/priv") {
		t.Fatal("manca mkdir delle cartelle share")
	}
	if !r.ran("systemctl enable --now smbd nmbd") {
		t.Fatal("smbd/nmbd non avviati con share abilitate")
	}
}
