// Package filemgr implementa le operazioni del file manager via browser. Ogni
// percorso è confinato (jailed) sotto una root consentita per impedire path
// traversal al di fuori delle aree gestite dal NAS.
package filemgr

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry descrive un elemento del filesystem mostrato nella UI.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"` // path relativo alla root
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"` // es. "rwxr-xr-x"
	ModTime time.Time `json:"mod_time"`
}

// Manager opera entro una directory radice consentita.
type Manager struct {
	root string // es. /srv/nas
}

func NewManager(root string) *Manager {
	abs, _ := filepath.Abs(root)
	return &Manager{root: abs}
}

// resolve traduce un path relativo richiesto dalla UI in path assoluto sicuro.
// Rifiuta qualsiasi tentativo di uscire dalla root (path traversal).
func (m *Manager) resolve(rel string) (string, error) {
	clean := filepath.Clean("/" + strings.TrimPrefix(rel, "/"))
	abs := filepath.Join(m.root, clean)
	if abs != m.root && !strings.HasPrefix(abs, m.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("percorso fuori dalla root consentita")
	}
	return abs, nil
}

// List elenca il contenuto di una directory.
func (m *Manager) List(rel string) ([]Entry, error) {
	abs, err := m.resolve(rel)
	if err != nil {
		return nil, err
	}
	items, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, it := range items {
		info, ierr := it.Info()
		if ierr != nil {
			continue
		}
		out = append(out, Entry{
			Name:    it.Name(),
			Path:    filepath.Join(rel, it.Name()),
			IsDir:   it.IsDir(),
			Size:    info.Size(),
			Mode:    info.Mode().Perm().String()[1:],
			ModTime: info.ModTime(),
		})
	}
	return out, nil
}

// Mkdir crea una nuova cartella.
func (m *Manager) Mkdir(rel string) error {
	abs, err := m.resolve(rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o755)
}

// Rename rinomina/sposta un elemento entro la root.
func (m *Manager) Rename(src, dst string) error {
	a, err := m.resolve(src)
	if err != nil {
		return err
	}
	b, err := m.resolve(dst)
	if err != nil {
		return err
	}
	return os.Rename(a, b)
}

// Remove elimina un file o una directory (ricorsivo).
func (m *Manager) Remove(rel string) error {
	abs, err := m.resolve(rel)
	if err != nil {
		return err
	}
	if abs == m.root {
		return fmt.Errorf("non è possibile eliminare la root")
	}
	return os.RemoveAll(abs)
}

// Chmod cambia i permessi POSIX di un elemento.
func (m *Manager) Chmod(rel string, mode os.FileMode) error {
	abs, err := m.resolve(rel)
	if err != nil {
		return err
	}
	return os.Chmod(abs, mode)
}

// Save crea (o sovrascrive) un file sotto la root e vi copia in streaming il
// contenuto di src, senza bufferizzarlo tutto in memoria (adatto a file grandi).
// Restituisce il numero di byte scritti. Il path è confinato via resolve.
func (m *Manager) Save(rel string, src io.Reader) (int64, error) {
	abs, err := m.resolve(rel)
	if err != nil {
		return 0, err
	}
	if abs == m.root {
		return 0, fmt.Errorf("percorso non valido")
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, src)
}

// OpenForDownload restituisce un *os.File aperto in lettura (l'handler imposta
// gli header e copia lo stream con io.Copy, gestendo file di grandi dimensioni).
func (m *Manager) OpenForDownload(rel string) (*os.File, os.FileInfo, error) {
	abs, err := m.resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, nil, fmt.Errorf("è una directory")
	}
	return f, info, nil
}
