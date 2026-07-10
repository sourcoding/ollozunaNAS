-- Migrazione iniziale dello schema nasd.
-- Gli account dell'interfaccia (app_users) sono distinti dagli utenti di
-- sistema Linux: servono per l'accesso alla web UI. Gli utenti di sistema
-- (gestiti via PAM/useradd) sono usati per share NFS/SMB e permessi POSIX.

CREATE TABLE IF NOT EXISTS app_users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Definizioni delle share gestite dalla UI. Sorgente di verità da cui si
-- rigenerano /etc/exports e la config Samba.
CREATE TABLE IF NOT EXISTS shares (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL,
    protocol    TEXT NOT NULL CHECK (protocol IN ('nfs','smb')),
    read_only   INTEGER NOT NULL DEFAULT 0,
    allowed_ips TEXT DEFAULT '',   -- CSV, per NFS
    valid_users TEXT DEFAULT '',   -- CSV, per SMB
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(name, protocol)
);

-- Cartelle multimediali per il server DLNA.
CREATE TABLE IF NOT EXISTS media_dirs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    path       TEXT NOT NULL UNIQUE,
    media_type TEXT DEFAULT ''     -- A/V/P o vuoto
);

-- Preferenze applicative (es. lingua di default, nome del NAS).
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT OR IGNORE INTO settings(key, value) VALUES ('default_locale', 'it');
INSERT OR IGNORE INTO settings(key, value) VALUES ('nas_name', 'NAS');
