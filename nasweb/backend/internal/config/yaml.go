package config

import (
	"strconv"
	"strings"
	"time"
)

// parseYAML è un parser intenzionalmente minimale: gestisce mappe annidate a 2
// livelli (sezione: \n  chiave: valore) sufficienti per questa config. Evita di
// trascinare una libreria YAML completa nel binario, in linea con i vincoli di
// leggerezza. Per config più complesse si può sostituire con gopkg.in/yaml.v3.
func parseYAML(data string, cfg *Config) error {
	var section string
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimRight(raw, "\r")
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		indented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		trimmed := strings.TrimSpace(line)
		key, val, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)

		if !indented && val == "" {
			section = key
			continue
		}
		assign(cfg, section, key, val)
	}
	return nil
}

func assign(cfg *Config, section, key, val string) {
	switch section {
	case "server":
		switch key {
		case "listen":
			cfg.Server.Listen = val
		case "tls_cert":
			cfg.Server.TLSCert = val
		case "tls_key":
			cfg.Server.TLSKey = val
		}
	case "database":
		switch key {
		case "path":
			cfg.Database.Path = val
		case "migrations_dir":
			cfg.Database.MigrationsDir = val
		}
	case "security":
		switch key {
		case "session_ttl":
			if d, err := time.ParseDuration(val); err == nil {
				cfg.Security.SessionTTL = d
			}
		case "bcrypt_cost":
			if n, err := strconv.Atoi(val); err == nil {
				cfg.Security.BcryptCost = n
			}
		case "csrf_enabled":
			cfg.Security.CSRFEnabled = val == "true"
		}
	case "paths":
		switch key {
		case "static_dir":
			cfg.Paths.StaticDir = val
		case "samba_conf":
			cfg.Paths.SambaConf = val
		case "exports":
			cfg.Paths.Exports = val
		case "minidlna_conf":
			cfg.Paths.MiniDLNAConf = val
		}
	}
}
