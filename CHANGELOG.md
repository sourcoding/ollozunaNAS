# Changelog

Formato ispirato a [Keep a Changelog](https://keepachangelog.com/).
L'ISO installabile è pubblicata come
[release GitHub](https://github.com/sourcoding/ollozunaNAS/releases); il nome file
è `ollozunaOS-<versione>.iso` e la versione si auto-incrementa a ogni build
(`nasweb/ISO_VERSION`).

## [0.1.2] — 2026-07-14

Prima release pubblicata su GitHub.

### Fixed
- **qBittorrent "access denied" sui download** (installazioni fisiche, non
  riproducibile sulla VM di sviluppo). Due cause distinte:
  - `nasd` girava in un mount namespace privato (`ProtectSystem` + `PrivateTmp`)
    con propagazione *slave*: i volumi montati a runtime erano visibili solo a
    `nasd`, mentre `qbittorrent.service` vedeva `/srv/nas/<vol>` vuoto. Aggiunto
    `MountFlags=shared` alla unit di `nasd`.
  - `fstab` usava `/dev/mdN`; al boot `mdadm` può riassemblare l'array come
    `/dev/md127`, facendo fallire (silenziosamente, per `nofail`) il mount. Ora la
    sorgente è `UUID=<fs>`, stabile a prescindere dal nome dell'array.

### Changed
- Installer: GRUB su chiavetta USB più veloce — `grub-installer/update-nvram=false`
  + `force-efi-extra-removable=true` (niente scrittura NVRAM in timeout).

## Build interne precedenti

- **0.1.1** (~2026-07-12) — share: accesso in lettura al contenuto qBittorrent
  condiviso; persistenza opzioni avanzate SMB/NFS; "Browse" su tutti i campi path.
- **0.1.0** (~2026-07-10) — prima ISO installabile: `nasd` (backend Go) + frontend
  Preact, repo APT locale, `live-build`, preseed che partiziona solo il disco di
  sistema. MVP funzionale (auth, utenti, share NFS/SMB, file manager, RAID +
  S.M.A.R.T., DLNA, WebSocket, i18n IT/EN).

[0.1.2]: https://github.com/sourcoding/ollozunaNAS/releases/tag/v0.1.2
