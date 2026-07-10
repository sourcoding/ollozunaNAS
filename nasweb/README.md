# NAS Web Management Interface (nasd)

Alternativa leggera a Synology DSM per hardware x86 Intel/AMD anche datato (RAM
minima 4 GB). Backend Go a basso consumo, frontend SPA Preact senza bundler,
distribuibile come ISO Debian minimal.

*A lightweight DSM-style management interface for Linux NAS systems. Go backend,
Preact SPA, packaged as a minimal Debian ISO. Bilingual IT/EN.*

---

## Architettura / Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Browser  ──HTTPS/WSS──►  nasd (Go, binario statico)      │
│  Preact SPA               REST API + WebSocket            │
│                           │                                │
│                           ├─ system.Runner (exec sicuro)   │
│                           │    ├─ mdadm   (RAID)            │
│                           │    ├─ smartctl(S.M.A.R.T.)      │
│                           │    ├─ samba / exportfs (share)  │
│                           │    ├─ useradd…  (utenti PAM)    │
│                           │    └─ minidlna  (DLNA)          │
│                           └─ SQLite (config, app_users)     │
└─────────────────────────────────────────────────────────┘
```

Principi chiave:
- **Footprint minimo**: binario Go statico, SPA senza framework pesanti, SQLite.
- **Sicurezza**: sessioni con cookie HttpOnly+Secure, CSRF double-submit,
  validazione rigorosa di ogni input prima di toccare il sistema, generazione
  dei file di config (mai concatenazione di stringhe utente nei comandi).
- **Affidabilità**: conferma esplicita per operazioni distruttive (RAID/share),
  validazione (`testparm`, `exportfs`) prima dell'applicazione, scritture
  atomiche dei file di configurazione.

## Struttura del progetto

```
backend/
  cmd/nasd/        entry point del daemon
  cmd/nasctl/      CLI admin (crea utente amministratore)
  internal/
    api/           router e handler HTTP
    auth/          password bcrypt, sessioni, CSRF
    config/        config YAML, SQLite, migrazioni
    middleware/    auth, CSRF, security headers, recover
    system/        runner comandi con allowlist + validatori
    users/         utenti/gruppi di sistema (PAM/passwd)
    shares/        NFS (exports) e SMB (Samba)
    filemgr/       file manager con jail anti path-traversal
    raid/          mdadm + S.M.A.R.T.
    dlna/          MiniDLNA
    ws/            hub WebSocket eventi real-time
  migrations/      schema SQL
frontend/
  public/          index.html
  src/js/          app Preact, client API, engine i18n
  src/i18n/        it.js, en.js
  src/css/         stile
scripts/
  build.sh         build backend + frontend -> ./dist
  build-iso.sh     ISO Debian minimal (live-build)
  nasd.service     unit systemd con hardening
docs/
```

## Build e installazione

### 1. Compilazione

Prerequisiti: Go 1.22+. **Nessun gcc/CGO** (SQLite è `modernc.org/sqlite`, puro
Go → binari statici). Node non richiesto. Gli asset frontend (Preact/HTM) sono
vendored in `frontend/public/assets/vendor`; per aggiornarli:
`./scripts/vendor-frontend.sh`.

```bash
cd backend && go mod download   # scarica le dipendenze
cd .. && ./scripts/build.sh      # binari statici + asset -> ./dist
```

### 2a. Pacchetto Debian (consigliato)

```bash
./scripts/build.sh
./scripts/build-deb.sh           # -> deb-build/nasd_<ver>_amd64.deb
sudo apt install ./deb-build/nasd_0.1.0_amd64.deb
# postinst: abilita e avvia nasd-firstboot (genera TLS) e nasd.
sudo nasctl create-admin -u admin -p 'CAMBIA_QUESTA_PASSWORD'
```

### 2b. Installazione manuale (senza pacchetto)

```bash
sudo cp -r dist/* /
sudo systemctl daemon-reload
sudo systemctl enable --now nasd-firstboot nasd
sudo nasctl create-admin -u admin -p 'CAMBIA_QUESTA_PASSWORD'
```

`nasd-firstboot` genera un certificato TLS self-signed e stampa in console l'URL.
Interfaccia su `https://<ip-del-nas>:8443`.

### 3. Immagine ISO

Richiede un host Debian/Ubuntu con **root** e `live-build`, `debootstrap`, `xorriso`.

```bash
sudo apt-get install live-build
./scripts/build.sh            # binari + asset -> ./dist
./scripts/build-deb.sh        # -> deb-build/nasd_<ver>_amd64.deb
GPG_KEY_ID=<chiave> \
  ./scripts/build-apt-repo.sh # repo APT locale firmato -> ./apt-repo
sudo ./scripts/build-iso.sh   # live-build -> iso-build/live-image-amd64.hybrid.iso
```

L'ISO installa `nasd` dal repo locale insieme ai servizi NAS, include il repo
anche nel sistema (`/opt/nasd-repo`, per upgrade offline) e usa `preseed.cfg`
per partizionare **solo il disco di sistema** (i dischi dati restano intatti).

Runbook completo per il build host (prerequisiti, firma repo, test in VM,
scrittura su USB): **`docs/BUILD_HOST.md`**.

## Localizzazione

Interfaccia IT/EN con selettore in alto e persistenza via cookie
`nasd_locale`. Per aggiungere una lingua: creare `frontend/src/i18n/<xx>.js`
con la stessa struttura di chiavi e registrarla in `src/js/i18n.js`.

## Stato di implementazione

| Modulo            | Backend | API | Frontend |
|-------------------|:-------:|:---:|:--------:|
| Auth / sessioni   |   ✅    | ✅  |    ✅    |
| i18n IT/EN        |   —     | —   |    ✅    |
| Utenti / gruppi   |   ✅    | ✅  |    ✅    |
| RAID (mdadm)      |   ✅    | ✅  |    ✅    |
| S.M.A.R.T.        |   ✅    | ✅  |    ✅    |
| Share NFS/SMB     |   ✅    | ✅  |    ✅    |
| File manager      |   ✅    | ✅  |    ✅    |
| DLNA              |   ✅    | ✅  |    ✅    |
| WebSocket eventi  |   ✅    | ✅  |    ✅    |

✅ funzionante

**Fase 1 (MVP) completata**: tutte le funzionalità del prompt sono operative
end-to-end (handler HTTP + viste Preact + test backend). I prossimi passi
(packaging, ISO, hardening) sono nel `docs/PIANO_PROGETTO_DISTRO_LINUX.md`.

## Sicurezza — note operative

- Generare certificati TLS reali (Let's Encrypt o CA interna) prima dell'uso in
  produzione; senza TLS i cookie non sono `Secure`.
- ✅ `chpasswd` riceve la password via stdin: `system.Runner.RunStdin` è
  implementato e usato da `users.SetPassword` (con guard anti line-injection).
- Valutare privilege separation: un helper minimale setuid/sudo per i soli
  comandi privilegiati, lasciando `nasd` come utente non-root.
