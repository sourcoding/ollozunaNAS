# Roadmap

Stato: **Fase 1 (MVP funzionale) completata.** Tutte le funzionalità del prompt
sono operative end-to-end (auth, utenti, share NFS/SMB, file manager, RAID +
S.M.A.R.T., DLNA, WebSocket, i18n IT/EN), coperte da test backend. I prossimi
passi riguardano packaging, ISO e hardening (vedi `PIANO_PROGETTO_DISTRO_LINUX.md`).

## Completato

- ✅ **`system.Runner` con input su stdin** (`RunStdin`)
  Implementato in `internal/system/runner.go` (`Run` e `RunStdin` condividono il
  metodo privato `exec`; stdin mai loggato). `users.SetPassword` ora passa
  `utente:password\n` a `chpasswd` via stdin con guard anti line-injection.
  Coperto da test (`internal/system`, `internal/users`).

- ✅ **Handler HTTP per share** (`sharesHandlers` in `internal/api`)
  GET/POST/PUT/DELETE `/api/shares` con persistenza nella tabella `shares`
  (sorgente di verità) e ri-applicazione via `shares.ApplyNFS`/`ApplySMB` a ogni
  modifica. Mutazioni solo admin; conferma richiesta per disabilitazione (PUT
  `enabled=false` + `confirm`) ed eliminazione (`?confirm=true`); validazione
  nome/percorso/protocollo. Include Samba derivato dalla dir di `samba_conf`
  (non più hardcoded). Coperto da test (`internal/api`).

- ✅ **Handler HTTP per file manager** (`fileHandlers` in `internal/api`)
  GET `/api/files?path=` (list), GET `/api/files/download` (`io.Copy` +
  `Content-Disposition`), POST mkdir/rename/chmod, POST `/api/files/upload`
  (multipart in streaming via `MultipartReader`, evento `file.progress` su
  WebSocket), DELETE con `?confirm=true`. Confinato sotto `paths.files_root`
  (jail di `filemgr`), riservato agli admin. `filemgr.Save` aggiunto per lo
  streaming su disco. Coperto da test, incluso il blocco del path traversal.

- ✅ **Viste Preact** per share e file manager (`SharesView`, `FilesView`).
  Share: tabella + form di creazione, enable/disable e delete con conferma.
  File manager: navigazione a breadcrumb, upload (pulsante + drag&drop) con
  progress bar via WebSocket, download, mkdir, rename, chmod, multi-selezione
  con eliminazione di gruppo.

- ✅ **DLNA** (`dlnaHandlers` + `DlnaView`)
  GET/POST/DELETE `/api/dlna/dirs` (tabella `media_dirs` come sorgente di verità,
  rigenerazione di `minidlna.conf` via `dlna.Configure` con `nas_name` come
  friendly_name) e POST `/api/dlna/rescan`. Vista Preact con gestione cartelle
  multimediali e re-scan. Solo admin. Coperto da test (`internal/api`).

- ✅ **UI S.M.A.R.T.** (`SmartPanel` in `RaidView`)
  Pannello di diagnostica per disco (salute/modello/temperatura) sull'endpoint
  `/api/raid/disks/{device}/smart` già esistente. i18n IT/EN in parità (100 chiavi).

### Fase 2 — Packaging

- ✅ **SQLite puro Go** (`modernc.org/sqlite` v1.34.4, AD-4) — `go-sqlite3`/CGO
  rimosso. `CGO_ENABLED=0 go build` produce un binario **statico** (verificato:
  `not a dynamic executable`); tutti i test passano con CGO disabilitato.
- ✅ **Asset frontend vendored** (AD-10) — Preact/HTM serviti da `/assets/vendor`
  via import map in `index.html` (no CDN, NAS offline-ready).
  `scripts/vendor-frontend.sh` li scarica; `app.js` usa specifier bare.
- ✅ **Pacchetto `.deb`** (AD-3) — `scripts/build-deb.sh` produce
  `nasd_<ver>_amd64.deb` (control/conffiles/postinst/prerm/postrm, unit in
  `/lib/systemd/system`, `Recommends` su mdadm/samba/nfs/smartmontools/minidlna).
  Verificato con `dpkg-deb`.
- ✅ **`nasd-firstboot`** (AD-7) — unit oneshot + script: genera TLS self-signed,
  prepara le dir dati, stampa URL e comando di creazione admin.

**Milestone M2 raggiunta**: `apt install ./nasd_*.deb` installa, abilita e avvia il
servizio su una Debian pulita.

### Fase 3 — Build distribuzione / ISO (in corso)

- ✅ **Repo APT locale firmato** (`scripts/build-apt-repo.sh`) — `apt-ftparchive`
  + `dpkg-scanpackages` producono `Packages`/`Release`; firma GPG opzionale.
  **Verificato**: `InRelease` → *Good signature*, `Packages` contiene `nasd`.
- ✅ **Config `live-build`** (`scripts/build-iso.sh` riscritto) — Debian stable
  minimal amd64, ISO ibrida, installer live; installa `nasd` dal repo locale con
  i servizi NAS (mdadm/samba/nfs/minidlna/nftables); repo incluso anche nel
  sistema (`/opt/nasd-repo`) per upgrade offline; hook abilita i servizi nasd;
  branding (hostname/MOTD). Validato `bash -n`.
- ✅ **Preseed installer** (`scripts/preseed.cfg`) — partiziona **solo il disco
  di sistema** (`partman-auto/disk`), i dischi dati non elencati restano intatti (AD-6).
- ⏳ **`lb build`** (Milestone M3) — richiede host Debian con **root** + `live-build`,
  `debootstrap`, `xorriso` (non disponibili nell'ambiente di sviluppo corrente:
  Ubuntu WSL2 senza sudo). Da eseguire su una macchina di build dedicata.

## Priorità media

5. **Gestione filesystem sui volumi RAID**: `mkfs`, mount, fstab, mount automatico
   (estendere `raid` o nuovo package `storage`).
6. **Quote utente/gruppo** (setquota/repquota) nel modulo users.
7. **Polling/parse `/proc/mdstat`** per `sync_pct` reale durante il rebuild,
   con push periodico via `hub.Emit("raid.status", …)`.
8. **Notifiche** stato array degradato (UI + opzionale email/webhook).

## Priorità bassa / hardening

9. Privilege separation: spostare i comandi privilegiati in un helper dedicato (AD-9).
10. CheckOrigin stringente sul WebSocket.
12. Test: estendere la copertura (più moduli con `system.Runner` mockato).

## Idee future

- Snapshot/Btrfs o ZFS opzionale.
- Backup pianificati (rsync/restic) con UI.
- Docker/Podman app manager leggero.
- Aggiunta lingue (struttura i18n già estendibile).
