// Package config gestisce il caricamento della configurazione, l'apertura
// del database SQLite e l'esecuzione delle migrazioni dello schema.
package config

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	// modernc.org/sqlite è un driver SQLite in puro Go (nessun CGO): permette di
	// compilare nasd come binario statico con CGO_ENABLED=0, semplificando il
	// packaging .deb e l'inclusione nell'ISO. Si registra come driver "sqlite".
	_ "modernc.org/sqlite"
)

// Config è la configurazione completa del daemon.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Security SecurityConfig `yaml:"security"`
	Paths    PathsConfig    `yaml:"paths"`
}

type ServerConfig struct {
	Listen  string `yaml:"listen"`   // es. ":8443"
	TLSCert string `yaml:"tls_cert"` // percorso certificato; vuoto = HTTP
	TLSKey  string `yaml:"tls_key"`
}

type DatabaseConfig struct {
	Path          string `yaml:"path"`
	MigrationsDir string `yaml:"migrations_dir"`
}

type SecurityConfig struct {
	SessionTTL  time.Duration `yaml:"session_ttl"`
	BcryptCost  int           `yaml:"bcrypt_cost"`
	CSRFEnabled bool          `yaml:"csrf_enabled"`
}

type PathsConfig struct {
	StaticDir    string `yaml:"static_dir"`    // SPA compilata
	SambaConf    string `yaml:"samba_conf"`    // /etc/samba/smb.conf
	Exports      string `yaml:"exports"`       // /etc/exports
	MiniDLNAConf string `yaml:"minidlna_conf"` // /etc/minidlna.conf
	FilesRoot    string `yaml:"files_root"`    // root jail del file manager, es. /srv
}

// Load legge e valida la configurazione. Se il file non esiste applica i
// default ragionevoli, così il sistema parte anche senza config esplicita.
func Load(path string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // usa i default
		}
		return nil, err
	}

	// Parser YAML minimale interno per evitare dipendenze pesanti.
	// Supporta solo chiavi note (vedi parseYAML).
	if err := parseYAML(string(data), cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Security.SessionTTL <= 0 {
		cfg.Security.SessionTTL = 8 * time.Hour
	}
	if cfg.Security.BcryptCost == 0 {
		cfg.Security.BcryptCost = 12
	}
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{Listen: ":8443"},
		Database: DatabaseConfig{
			Path:          "/var/lib/nasd/nasd.db",
			MigrationsDir: "/usr/share/nasd/migrations",
		},
		Security: SecurityConfig{
			SessionTTL:  8 * time.Hour,
			BcryptCost:  12,
			CSRFEnabled: true,
		},
		Paths: PathsConfig{
			StaticDir:    "/usr/share/nasd/www",
			SambaConf:    "/etc/samba/smb.conf",
			Exports:      "/etc/exports",
			MiniDLNAConf: "/etc/minidlna.conf",
			FilesRoot:    "/srv",
		},
	}
}

// OpenDB apre SQLite con impostazioni adatte a basso footprint e concorrenza
// moderata (WAL, foreign keys on, busy timeout).
func OpenDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	// Sintassi pragma di modernc.org/sqlite (diversa da go-sqlite3).
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: una sola connessione di scrittura evita lock
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}

// Migrate applica in ordine i file .sql presenti in dir, tenendo traccia in una
// tabella schema_migrations. Idempotente.
func Migrate(db *sql.DB, dir string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory migrazioni non trovata: %s", dir)
		}
		return err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, f := range files {
		var exists int
		_ = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, f).Scan(&exists)
		if exists > 0 {
			continue
		}
		sqlBytes, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrazione %s: %w", f, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, f); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
