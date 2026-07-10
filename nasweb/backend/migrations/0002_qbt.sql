-- Configurazione dell'app qBittorrent (riga singola, id=1).
CREATE TABLE IF NOT EXISTS qbt_config (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    configured    INTEGER NOT NULL DEFAULT 0,
    temp_dir      TEXT NOT NULL DEFAULT '',
    torfile_dir   TEXT NOT NULL DEFAULT '',
    downloads_dir TEXT NOT NULL DEFAULT '',
    perms_mode    TEXT NOT NULL DEFAULT '2770',
    webui_port    INTEGER NOT NULL DEFAULT 8080,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT OR IGNORE INTO qbt_config(id, configured) VALUES (1, 0);
