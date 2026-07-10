-- Persistenza della configurazione avanzata delle share (opzioni SMB e NFS)
-- come blob JSON. Prima solo name/path/read_only/valid_users/allowed_ips/enabled
-- venivano salvati: tutte le altre impostazioni della UI (versione SMB, signing,
-- guest, recycle bin, versioni/regole NFS, ...) andavano perse al salvataggio.
-- Con questa colonna l'intera configurazione viene conservata e riapplicata.
ALTER TABLE shares ADD COLUMN config TEXT NOT NULL DEFAULT '';
