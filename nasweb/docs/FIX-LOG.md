# FIX-LOG — sessione debug share / RAID / boot

Elenco delle modifiche applicate. Deploy a caldo sulla VM di test (`vm/disks/usb.qcow2`
via `qemu-nbd`) ad ogni giro; **rebuild ISO solo alla fine**, quando tutti i bug GUI
sono chiusi.

## Share NFS / CIFS
- **EBUSY su create** (`rename /etc/exports.tmp: device or resource busy`):
  `scripts/nasd.service` + `dist/lib/systemd/system/nasd.service` — `ProtectSystem=full`→`true`,
  rimosso `ReadWritePaths` (i bind-mount su singoli file rompono il rename atomico).
- **CIFS non visibile a Samba**: `scripts/build-iso.sh` hook — `include = /etc/samba/nasd-shares.conf`
  + `map to guest = Bad User` in `[global]`; crea `nasd-shares.conf`/`/etc/exports`.
- **Servizi non avviati**: `internal/shares/shares.go` — `ApplyNFS` avvia/ferma `nfs-server`,
  `ApplySMB` avvia/ferma `smbd nmbd` in base alle share abilitate; reload non fatale.
- **Cartella share mancante**: `shares.go` — `mkdir -p` + `chmod 0777` del path per ogni share.
- **CIFS guest vs autenticata**: `shares.go` — se nessun valid users → `guest ok = yes`,
  altrimenti `valid users = …`; + `create/directory mask`. Frontend `mapCIFSToAPI`: toggle guest → pubblica.
- **Autenticazione CIFS**: `internal/users/users.go` — `SetPassword` sincronizza anche `smbpasswd`.
- **NFS "add client rule" / "create export" bloccati**: `frontend/app.js` — `NFSModal.setRules`
  ora accetta updater funzionale (prima `form.rules` diventava una funzione).
- **Messaggio duplicato leggibile**: `internal/api/handlers.go` — `isUniqueViolation` → 409 chiaro.
- **Finestre config strette**: `frontend/app.css` `modal-lg` 980px + larghezze inline.

## RAID / Filesystem
- **Creare RAID rompe il boot (emergency mode)**: `internal/raid/raid.go` —
  `Create()` registra l'array in `/etc/mdadm/mdadm.conf` + `update-initramfs`;
  `fstabAdd()` usa `nofail,x-systemd.device-timeout=10s`; `Stop()` ripulisce `mdadm.conf`.
- **Firma FS stale dopo ricreazione (mount "corrupted")**: `Create()` fa `wipefs -a` sul nuovo array.
- **Cancellare il filesystem anche se non montato**: nuovo `WipeFilesystem` (umount+fstab+`wipefs -a`),
  rotta `DELETE /raid/arrays/{md}/filesystem`, bottone **🗑️ Delete FS** nel frontend (montato e non).
- **Delete FS rimuove anche la cartella del mount point**: `WipeFilesystem` fa `rmdir` del mount point
  (da /proc/mounts → fstab → default `/srv/nas/<dev>`); `rmdir` solo se vuota (sicuro).
- **Avviso cancellazione array montato**: `frontend/app.js` — messaggio con mount point + FS.

## Installer / VM (non-GUI, per completezza)
- `scripts/preseed.cfg`: disco **scelto** durante l'install (no hardcode `/dev/sda`);
  password di `nasadmin` **chiesta** all'install; `nasadmin` in `sudo`.
- `scripts/build-iso.sh`: aggiunti `smbclient cifs-utils nfs-common openssh-server` alla package-list.
- `run-vm.sh`: a prova di errore, chiavetta USB, boot `bootindex`, rete bridge, `NET/USB/GUI` env.

## Verifica end-to-end (fatta)
API create NFS+CIFS → 201; CIFS guest+auth `smbclient` put OK; NFS `showmount`+mount+write OK.

## Utenti (GUI)
- **Utente esistente (es. `admin`) senza password Samba per CIFS**: nell'handler `create` la
  `SetPassword` (che sincronizza anche `smbpasswd`) partiva **solo se `CreateUser` riusciva**;
  con un account di sistema già presente `useradd` fallisce → password Samba mai impostata.
  Fix: `handlers.go` — ora `CreateUser` è best-effort e `SetPassword` viene chiamata **sempre**.
  (Per un utente già creato in precedenza: Edit User → imposta password → sincronizza Samba.)
- **Creazione utente: "Folder Permissions" mostrava share finte** (`/media,/backup,/documents,/public`
  hardcoded in `US_SHARES`): ora `PermsTable` elenca le **share reali** caricate da `api.listShares()`
  (passate a `AddEditUserModal` da `UsersView`); `sharePerms` init dinamica.
- **Finestra troppo stretta**: `app.css` `.modal-lg` ora ha `width:920px` (prima solo `max-width`,
  quindi restava 500px per via di `.modal-card{width:500px}`). Vale per tutte le modali `modal-lg`.

## TODO / da testare ancora nella GUI
- I permessi per-share nella creazione utente sono ora **coerenti** ma **non ancora persistiti**
  (il backend crea l'utente con username/password/is_admin; per applicarli servirebbe aggiungere
  l'utente ai `valid_users` delle share scelte). Da decidere se cablarli.
- (in aggiornamento man mano che emergono bug)

## DLNA (GUI)
- **Campo Path sfogliabile**: nuovo componente `FolderPicker` (naviga le cartelle sotto `/srv`
  via `api.listFiles`, con "Nuova cartella") + bottone **📁 Sfoglia** accanto al Path in `DlnaView`.

## RAID dischi (GUI)
- **Bottone "🧹 Wipe" per ogni disco disponibile**: azzera partizioni e firme (fs/RAID) del disco.
  Backend `WipeDisk` (`mdadm --zero-superblock` + `wipefs -a` + `partprobe`) con guardia
  `diskInUse` (rifiuta se montato o membro di array); rotta `POST /raid/disks/{device}/wipe`.
  Frontend: colonna Actions con **Wipe** + `WipeDiskModal` di avviso (dati/partizioni irreversibili).

## Wipe disco: RAID residua + lingua
- **Wipe rimuove la config RAID residua**: se il disco è ancora membro di un array md
  (anche auto-assemblato e non mostrato nella UI, es. `md127`), `WipeDisk` ora lo **ferma**
  (`mdadm --stop` + pulizia `mdadm.conf`) e poi azzera superblock/firme. Rifiuta solo se
  il disco o l'array è **montato**. Nuovi helper `isMounted`/`arraysForDisk` (parsing /proc/mdstat).
- **Uniformate le scritte GUI in inglese** (le nuove stringhe erano in italiano → mix):
  WipeDiskModal, FolderPicker, "Browse", PermsTable, confirm Delete FS, messaggi errore WipeDisk.

## RAID: nascondi il disco di sistema
- **Il disco che ospita l'OS (`/`) non compare più nella gestione RAID**: `ListDisks` esclude
  il disco della root (rilevato con `findmnt -no SOURCE /` + `lsblk -no PKNAME`). Evita di
  usarlo per array o di wiparlo per errore. Nuovo helper `osDisk`.

## Branding (logo)
- **Logo nel login + icona nella sidebar**: `frontend/public/assets/img/{logo,logo-icon}.png`
  (da `docs/ollozuna_logo1.png` / `ollozuna_logo_icon.png`); `build.sh` copia `assets/img/`.
  Login: `<img class="login-logo">`; sidebar `brand`: `<img class="brand-icon">` + titolo.
  CSS: `.login-logo` (150px, centrato), `.brand` flex + `.brand-icon` (30px).
- **Logo login → `ollozuna_logo1gr.png`** (sostituisce `assets/img/logo.png`); sidebar: titolo
  "OLLOZUNA / NAS Manager" su 2 righe (`.brand-text`), icona 36px.

## DLNA: Media Library
- **Pannello "Media Library" sotto DLNA**: mostra i file condivisi raggruppati in tab
  **Video / Pictures / Music**. Backend `GET /dlna/files` percorre le cartelle configurate e
  classifica i file per estensione (`mediaExt`), cap 1000/cat. Frontend: componente `MediaLibrary`.

## App qBittorrent — Incremento 1 (gating + stato)
- **Macchina a stati** `internal/qbt`: unavailable→available→configured→running/stopped, calcolata
  dai **volumi dati montati** (`raid.ListFilesystems`, nessun path hardcoded). `Reconcile` ferma
  il servizio se spariscono i volumi.
- **API** `GET /apps/qbt/status`, `GET /apps/qbt/volumes`; tabella `qbt_config` (migrazione 0002).
- **Frontend**: voce di menu **qBittorrent** disabilitata (greyed + tooltip "Crea almeno un volume…")
  finché non esiste un volume; `QbtView` mostra lo stato; polling 8s per gating a caldo. i18n it/en.
- Pacchetti ISO: `qbittorrent-nox`, `acl`. `deploy.sh` ora copia anche le migrazioni.
- Prossimi incrementi: wizard 6-step + file browser ristretto ai volumi; operazioni privilegiate
  (create-dir/chown/setacl) + verifica accesso; config qBittorrent + avvio servizio; reconfigure/migrazione.

## App qBittorrent — Incremento 2/3 (wizard + configure)
- **Wizard 6-step** (`QbtWizard`) + **file browser ristretto ai volumi** (`QbtFolderPicker`,
  API `/apps/qbt/browse|mkdir` — mai rootfs/etc/var; canonicalizza realpath ⊂ volume, no traversal).
  Default su primo volume: `/<vol>/qbittorrent/{temp,torrents,downloads}`.
- **Validazione** (`/validate`): dentro volumi, distinte, temp≠downloads, spazio libero.
- **Configure** (`/configure`, idempotente): installa `qbittorrent-nox`+`acl`, crea utente
  `qbtuser`:`nas-media`, per ogni dir mkdir+chown+chmod(2770/2775)+setgid+`setfacl -d`+verifica
  accesso reale con `runuser -u qbtuser test -w`; scrive `qBittorrent.conf` (TempPath/DefaultSavePath/
  TorrentExportDirectory/ScanDirs), password WebUI **PBKDF2 random** mostrata una volta; unit systemd
  `qbittorrent.service` (User=qbtuser) + `enable --now` su porta scelta. Start/Stop/Reconfigure in UI.

## Menu Amministrazione (shutdown / reboot / log)
- **Nuova voce di menu "Amministrazione"** in sidebar, visibile **solo agli admin** (`user.is_admin`).
  `AdminView` (`frontend/src/js/app.js`) con due card: alimentazione + visualizzatore log.
- **Backend** `internal/system/sysmgmt.go` (`Management`): `Reboot`/`Shutdown` via
  `systemctl --no-block reboot|poweroff` (risposta HTTP prima della caduta connessione);
  `Logs(unit, lines)` via `journalctl -o short-iso -n N [-u unit]` con parsing in `LogEntry`
  (time/unit/message/level). `unit` **validato** contro whitelist `KnownUnits`
  (nasd, smbd, nmbd, nfs-server, minidlna, qbittorrent, ssh) → 422 se non valido. `lines` clampato 1..2000.
- **API** `internal/api/admin.go` + route `/api/admin/{logs,reboot,shutdown}` tutte `RequireAdmin`+CSRF.
- **Frontend**: bottoni Riavvia/Spegni con **modale di conferma** (danger); log viewer con filtro
  per servizio + numero righe (100/300/500/1000), refresh, evidenziazione error/warn (CSS `.log-view`).
- i18n it/en (`nav.admin`, sezione `admin.*`). Test `sysmgmt_test.go` (parseJournal short-iso, validUnit).
- Verificato sulla VM: logs (parse timestamp `+02:00`, filtro unit, 422 su unit invalida) e **reboot
  end-to-end** (VM caduta e riavviata). shutdown identico (`poweroff`), non triggerato per non dover
  riaccendere manualmente.

## Installazione GRUB lenta (os-prober)
- **Causa**: durante "installazione di GRUB" `os-prober` monta e scandisce ogni
  partizione di ogni disco dati (RAID/storage) per cercare altri OS → molto lento
  su un NAS con più dischi.
- **Fix installer** (`scripts/preseed.cfg`): `d-i grub-installer/with_other_os boolean false`
  salta os-prober durante l'install (sistema single-boot).
- **Fix persistente** (`scripts/build-iso.sh`, hook chroot): imposta
  `GRUB_DISABLE_OS_PROBER=true` in `/etc/default/grub` così anche gli `update-grub`
  successivi (es. dopo aggiornamento kernel) restano veloci.

## Create Filesystem btrfs — "mkfs.btrfs: executable file not found in $PATH"
- **Causa**: la UI offre ext4/btrfs/xfs e il backend chiama il rispettivo `mkfs.*`,
  ma l'immagine installava solo `e2fsprogs` (ext) e `xfsprogs` (xfs). Mancava
  `btrfs-progs` → `mkfs.btrfs` non in $PATH → errore alla creazione del filesystem btrfs.
- **Fix 1 (immagine)** `scripts/build-iso.sh`: aggiunto `btrfs-progs` alla package-list,
  così ogni tipo filesystem offerto in UI ha il proprio mkfs preinstallato.
- **Fix 2 (runtime, on-demand)** `internal/raid/raid.go` `ensureMkfs()`: prima di
  `mkfs`, se il comando manca nasd installa il pacchetto corrispondente
  (e2fsprogs/btrfs-progs/xfsprogs) via `systemd-run`+apt (fuori dalla sandbox
  ProtectSystem). Idempotente. Così btrfs funziona anche su un sistema già installato
  senza il pacchetto, senza reinstallare. Messaggio d'errore chiaro se manca la rete.

## RAID orfani: "la config resta" dopo cancellazione / blocco GRUB in install
Due facce dello stesso problema (superblock mdadm che sopravvivono e fanno
riassemblare array orfani, es. /dev/md127):
- **Cancellazione array dalla UI** (`internal/raid/raid.go` `Stop()`): oltre a
  `mdadm --stop` ora rileva i dischi membri (`arrayMembers` via `mdadm --detail`,
  PRIMA dello stop) e su ognuno esegue `mdadm --zero-superblock` + `wipefs -a` +
  `partprobe`. Così l'array cancellato NON riappare al boot.
- **Installazione da ISO** (`scripts/preseed.cfg`, `partman/early_command`): appena
  prima del partizionamento ferma tutti gli array e azzera superblock + firme
  (testa e coda) su ogni disco fisso, rendendoli vergini. Evita che grub-installer
  si blocchi sui "raid orfani". Esclude il supporto d'installazione (/cdrom o live
  medium). Azione distruttiva, coerente con NAS che riconfigura lo storage da UI.

### Scrub install: protezione del disco di boot (Ventoy USB->SATA)
Lo scrub install è stato spostato in `scripts/scrub-disks.sh` (incluso nell'initrd,
richiamato da `partman/early_command`) e reso sicuro sul disco di boot:
- **esclude tutti i dischi sul bus USB** — incluso un SSD SATA su convertitore
  USB->SATA con Ventoy a bordo (il supporto d'installazione dell'utente);
- **esclude il disco che ospita il medium**, risolto attraverso loop/device-mapper
  (es. `/dev/mapper/ventoy`) e partizioni fino al disco fisico.
Conservativo: nel dubbio NON cancella. Verificato con un mock sysfs che simula lo
scenario Ventoy USB->SATA: il disco di boot (sda, USB) è protetto da entrambe le
regole; i dischi dati interni SATA/NVMe vengono azzerati.

## Menu di boot ISO: installer come default + timeout 5s
- **Richiesta**: nel menu di boot iniziale dell'ISO, voce di default = installer,
  con avvio automatico dopo 5 secondi.
- **Fix** (`scripts/build-iso.sh`, hook binary `0200-bootmenu.hook.binary`): dopo
  la generazione dei config bootloader, imposta:
  - isolinux (BIOS): `timeout 50` (5s) + `ontimeout installstart`; sposta l'unico
    `menu default` dalla voce Live a `installstart`.
  - GRUB (UEFI): `set default='Start installer'`, `set timeout=5`, `timeout_style=menu`.
- Logica verificata sui config reali estratti dall'ISO (senza rebuild): default e
  timeout corretti su entrambi i bootloader. NB: modifica solo-ISO (non hot-deployabile
  sulla VM) → effettiva alla prossima build (v0.1.1).

## Browse button su tutti i campi path
- **Richiesta**: ogni campo che contiene un percorso deve avere un bottone "Browse".
- **Fix** (`frontend/src/js/app.js`): aggiunto il picker cartelle (`FolderPicker`,
  root `/srv` = FilesRoot) accanto ai campi path che ne erano privi:
  - `CreateFilesystemModal` — Mount Point;
  - `MountExistingModal` — Mount Point;
  - `CIFSModal` — Path (share SMB);
  - `NFSModal` — Export Path (share NFS).
  DLNA e wizard qBittorrent avevano già il browse (FolderPicker / QbtFolderPicker).
  Ogni bottone apre il picker e ne scrive il percorso assoluto nel campo.

## Share: le modifiche avanzate non venivano salvate (SMB version, ecc.)
- **Bug**: modificando una share (es. versione SMB) e salvando, l'impostazione non
  veniva mantenuta. Causa: solo name/path/read_only/valid_users/allowed_ips/enabled
  erano persistiti; tutte le opzioni avanzate della UI venivano scartate al save
  (`mapCIFSToAPI`/`mapNFSToAPI` le omettevano), non c'era colonna DB, e in lettura
  `mapShareFromAPI` fabbricava valori di default.
- **Fix — persistenza round-trip**:
  - DB: migrazione `0003_share_config.sql` → colonna `config TEXT` (blob JSON).
  - Backend (`internal/shares`, `internal/api/handlers.go`): `Share.Config` salvata/riletta
    (create/update/loadAll). Round-trip completo: tutto ciò che l'utente imposta è conservato.
  - Frontend (`app.js`): `mapCIFSToAPI`/`mapNFSToAPI` inviano `config: form`;
    `mapShareFromAPI` reidrata dal blob (fallback ai default per share vecchie).
- **Fix — applicazione reale** delle opzioni (generazione config):
  - SMB (`ApplySMB`): comment, hide dot files, case sensitive, oplocks, smb encrypt,
    `vfs objects = recycle` (cestino), `fruit` + time machine.
  - NFS (`ApplyNFS`): regole per-client con rw/ro, sync/async, subtree_check, secure,
    root_squash/no_root_squash/all_squash, crossmnt, anonuid/anongid.
  - Nota: la *versione SMB* (min protocol) e il *signing* sono parametri GLOBALI di
    Samba (non per-share): ora vengono conservati/riletti nella UI ma non emessi nel
    per-share include. Da valutare eventuale applicazione in [global] se richiesto.
- Test: `shares_apply_test.go` (SMB avanzato + regole NFS da config); api round-trip.

## Share su download qBittorrent: "access denied" riproducendo un video
- **Causa**: i download di qBittorrent sono di proprietà `qbtuser:nas-media` con
  `perms_mode=2770` (privato). L'albero (cartella `qbittorrent`, sottocartelle
  per-torrent, file) nega ogni bit "other". Chi accede via SMB/NFS opera come
  utente NON proprietario (o guest `nobody`), non è nel gruppo `nas-media`, quindi
  non può attraversare le directory né leggere i file → access denied. Il
  `chmod 0777` in `ApplySMB` toccava solo la cartella top della share (non gli
  antenati né il contenuto annidato).
- **Fix** (`internal/shares` `GrantShareAccess`, chiamato da create/update share):
  1. `o+x` su tutte le directory antenate → il percorso diventa raggiungibile;
  2. `chmod -R o+rX` sul sottoalbero → file leggibili, directory attraversabili.
  Non distruttivo (aggiunge solo bit "other"). I file creati dopo da qBittorrent
  sono già `o+r` grazie a `UMask=0002` nella unit. Vale per SMB e NFS.
- **Verificato live sulla VM**: creato albero restrittivo (dir 0770, file 0660,
  "other" negato) con un file; creata una share guest sopra → i permessi diventano
  attraversabili/leggibili; `smbclient -N` (guest, non in nas-media) **scarica il
  file con successo** (prima: access denied). Test: `shares_apply_test.go`.

## qBittorrent "access denied" su PC fisico: mount invisibile + array md127 al boot
- **Sintomo (installazione fisica, non VM)**: dopo aver configurato qBittorrent,
  TUTTI i download danno "access denied", sia con `perms_mode=2770` che `2775`.
  Le cartelle `qbittorrent/{temp,torrents,downloads}` non risultano nemmeno create
  dal punto di vista dello shell / di `qbittorrent.service`.
- **Causa 1 — mount intrappolato nel namespace di nasd**: `nasd.service` gira in un
  mount namespace privato (indotto da `ProtectSystem=true` + `PrivateTmp=true`) con
  propagazione `slave` di default. Il `mount` che nasd esegue in `CreateFilesystem`/
  `MountOnly` resta visibile SOLO a nasd; `qbittorrent.service` (namespace host) e le
  shell vedono `/srv/nas/<vol>` vuoto. Il wizard crea le dir sul volume montato dentro
  il namespace di nasd → qBittorrent scrive su un path che nel SUO namespace non esiste
  → access denied.
- **Fix 1**: `scripts/nasd.service` + `dist/lib/systemd/system/nasd.service` —
  aggiunto `MountFlags=shared`, così i mount di nasd propagano a tutto il sistema.
- **Causa 2 — array riassemblato come /dev/md127 al boot**: `fstabAdd` scriveva la
  sorgente come `/dev/mdN`. Al riavvio mdadm può riassemblare l'array con nome diverso
  (tipicamente `/dev/md127` invece di `md0`); la riga fstab su `/dev/md0` non trova il
  device → con `nofail` il volume resta smontato → il path dei download sparisce.
- **Fix 2**: `internal/raid/raid.go` `fstabAdd` — sorgente per `UUID=<fs>` (via
  `blkid -s UUID`), stabile a prescindere dal nome dell'array. `fstabRemove` continua
  a matchare per mount point, quindi resta compatibile.
- **Verificato live sulla VM** (deploy a caldo nasd+unit): stop qbt → unmount →
  **mount LIVE via API** (esercita MountFlags + fstab UUID) → reconfigure qbt (tutte
  le `applyDir` OK, dir 2775 `qbtuser:nas-media`) → **qBittorrent WebUI**: torrent
  completati tornano in *seeding* (legge i file dal volume montato live) e un torrent
  nuovo va in `stalledDL` **senza access denied** → prova la visibilità cross-namespace.
  Reboot reale (down→up): volume **auto-montato**, qbt *running*. `/etc/fstab` sul disco
  ora è `UUID=… /srv/nas/md0 ext4 nofail,…`; unit con `MountFlags=shared`.
- **NB installazione fisica esistente**: fix nel codice → vale per le NUOVE
  installazioni/filesystem. Su un box già installato: rendere il volume montato di
  sistema (fstab per UUID) e riavviare, oppure aggiornare nasd (binario + unit).
