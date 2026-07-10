// Package users gestisce utenti e gruppi di sistema Linux integrando i comandi
// useradd/usermod/userdel e leggendo /etc/passwd e /etc/group.
package users

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/nasweb/nasd/internal/system"
)

// User rappresenta un utente di sistema.
type User struct {
	Username string `json:"username"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Home     string `json:"home"`
	Shell    string `json:"shell"`
	Comment  string `json:"comment"`
}

// Group rappresenta un gruppo di sistema.
type Group struct {
	Name    string   `json:"name"`
	GID     int      `json:"gid"`
	Members []string `json:"members"`
}

type Manager struct {
	run    system.Runner
	minUID int // soglia per filtrare utenti di sistema dalla UI
}

func NewManager(run system.Runner) *Manager {
	return &Manager{run: run, minUID: 1000}
}

// ListUsers legge /etc/passwd e restituisce gli utenti "umani" (UID >= minUID).
func (m *Manager) ListUsers() ([]User, error) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var users []User
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), ":")
		if len(parts) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(parts[2])
		if uid < m.minUID && uid != 0 {
			continue
		}
		gid, _ := strconv.Atoi(parts[3])
		users = append(users, User{
			Username: parts[0], UID: uid, GID: gid,
			Comment: parts[4], Home: parts[5], Shell: parts[6],
		})
	}
	return users, sc.Err()
}

// ListGroups legge /etc/group.
func (m *Manager) ListGroups() ([]Group, error) {
	f, err := os.Open("/etc/group")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var groups []Group
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), ":")
		if len(parts) < 4 {
			continue
		}
		gid, _ := strconv.Atoi(parts[2])
		var members []string
		if parts[3] != "" {
			members = strings.Split(parts[3], ",")
		}
		groups = append(groups, Group{Name: parts[0], GID: gid, Members: members})
	}
	return groups, sc.Err()
}

// CreateUser crea un utente di sistema con home directory.
func (m *Manager) CreateUser(ctx context.Context, username, comment string) error {
	if !system.ValidUsername(username) {
		return fmt.Errorf("nome utente non valido")
	}
	args := []string{"-m", "-s", "/bin/bash"}
	if comment != "" {
		args = append(args, "-c", comment)
	}
	args = append(args, username)
	_, _, err := m.run.Run(ctx, "useradd", args...)
	return err
}

// DeleteUser elimina un utente (e opzionalmente la sua home).
func (m *Manager) DeleteUser(ctx context.Context, username string, removeHome bool) error {
	if !system.ValidUsername(username) {
		return fmt.Errorf("nome utente non valido")
	}
	args := []string{}
	if removeHome {
		args = append(args, "-r")
	}
	args = append(args, username)
	_, _, err := m.run.Run(ctx, "userdel", args...)
	return err
}

// SetPassword imposta la password via chpasswd, passando "user:password" sullo
// standard input (mai come argomento, dove sarebbe visibile in `ps`).
func (m *Manager) SetPassword(ctx context.Context, username, password string) error {
	if !system.ValidUsername(username) {
		return fmt.Errorf("nome utente non valido")
	}
	// chpasswd legge una riga per ogni coppia "utente:password". Una password
	// che contiene a-capo permetterebbe di iniettare righe aggiuntive e quindi
	// di cambiare la password di altri account: la rifiutiamo.
	if strings.ContainsAny(password, "\n\r") || password == "" {
		return fmt.Errorf("password non valida")
	}
	if _, _, err := m.run.RunStdin(ctx, username+":"+password+"\n", "chpasswd"); err != nil {
		return err
	}
	// Sincronizza la password Samba (tdbsam) per l'accesso CIFS: `smbpasswd -s`
	// legge la password due volte da stdin, `-a` crea l'utente se non esiste.
	// Best-effort: se Samba non è installato l'accesso NFS/login web resta ok.
	m.run.RunStdin(ctx, password+"\n"+password+"\n", "smbpasswd", "-s", "-a", username) //nolint:errcheck
	return nil
}

// CreateGroup crea un gruppo.
func (m *Manager) CreateGroup(ctx context.Context, name string) error {
	if !system.ValidUsername(name) {
		return fmt.Errorf("nome gruppo non valido")
	}
	_, _, err := m.run.Run(ctx, "groupadd", name)
	return err
}

// AddToGroup aggiunge un utente a un gruppo supplementare.
func (m *Manager) AddToGroup(ctx context.Context, username, group string) error {
	if !system.ValidUsername(username) || !system.ValidUsername(group) {
		return fmt.Errorf("parametri non validi")
	}
	_, _, err := m.run.Run(ctx, "usermod", "-aG", group, username)
	return err
}
