# Piano di Progetto — Distribuzione Linux NAS ("ollozunaOS")

> Obiettivo: produrre una **distribuzione Linux installabile (ISO)** che monti a bordo
> il software NAS `nasd` descritto in `prompt-nas-web-interface.md`. Alternativa
> leggera a Synology DSM per hardware x86 Intel/AMD anche datato (RAM ≥ 4 GB).
>
> Questo documento è il **registro vivo delle decisioni**: ogni scelta architetturale
> è annotata con la sua motivazione (rationale) e viene riflessa nel knowledge graph
> di graphify a ogni aggiornamento.

---

## 1. Scope e confini

**In scope**
- Software NAS `nasd` (backend Go + SPA Preact) — completamento dei moduli mancanti.
- Packaging del software come pacchetto `.deb` versionabile.
- Costruzione di un'immagine **ISO ibrida** (live + installabile) basata su Debian minimal.
- Installer headless con partizionamento del solo disco di sistema.
- Provisioning al primo avvio (TLS, admin, rete, hostname).
- Hardening a livello di distribuzione (firewall, privilege separation, servizi minimi).
- Documentazione IT/EN e artefatti di rilascio.

**Fuori scope (per la v1.0)**
- Cluster / alta disponibilità multi-nodo.
- Snapshot Btrfs/ZFS, backup pianificati, app-manager Docker (vedi *Idee future* in `ROADMAP.md`).
- Variante Alpine/musl (valutata come ramo futuro "ultra-light").

---

## 2. Vincoli (dal prompt)

| Vincolo | Valore target |
|---|---|
| Architettura | x86 / x86_64 Intel e AMD, incluse CPU datate |
| RAM minima | 4 GB |
| Footprint servizi a riposo | < ~512 MB |
| Avvio | rapido, priorità a leggerezza e basso consumo |
| Distribuzione | ISO Linux installabile |
| Lingue UI | Italiano + Inglese |

Questi vincoli sono i **criteri di accettazione non funzionali** verificati in Fase 6.

---

## 3. Decisioni architetturali chiave (con rationale)

> Ogni decisione qui sotto è un nodo `rationale` nel knowledge graph. Le decisioni
> aperte (status *Da confermare*) vanno chiuse prima della fase che le consuma.

### AD-1 — Base distro: **Debian stable minimal** ✅ deciso
**Scelta:** Debian 13 ("trixie") minimal, niente desktop.
**Perché:** glibc maturo e compatibile con CPU datate; `mdadm`, `samba`, `nfs-kernel-server`,
`minidlna`, `smartmontools` tutti nei repo ufficiali; toolchain `live-build` matura;
già adottato in `scripts/build-iso.sh`. Alpine (musl) sarebbe più leggero ma introduce
attrito con CGO/glibc e con i tool di sistema → rinviato a variante futura.

### AD-2 — Init system: **systemd** ✅ deciso
**Perché:** default Debian; `scripts/nasd.service` usa già hardening systemd
(`ProtectSystem`, `NoNewPrivileges`, ecc.). Nessun motivo per divergere.

### AD-3 — Packaging di `nasd`: **pacchetto `.deb`** ✅ deciso (sostituisce `cp -r dist/*`)
**Perché:** installazione tracciata, aggiornabile e disinstallabile; integrabile nell'ISO
via `apt`/repo locale; gli `postinst`/`prerm` gestiscono utente di servizio, unit systemd
e migrazioni. Migliora l'attuale copia "loose" descritta nel README.

### AD-4 — Driver SQLite: **modernc.org/sqlite (puro Go)** ✅ deciso
**Perché:** binario 100% statico senza CGO/gcc → packaging semplice, nessuna dipendenza
glibc runtime, build riproducibile. Già segnalato come obiettivo nella `ROADMAP.md` (punto 13).
Abilita anche la futura variante musl/Alpine.

### AD-5 — Installer: **Debian Installer (d-i) con preseed** ✅ deciso
**Perché:** NAS = server headless; un installer testuale preseedato è affidabile e leggero.
Calamares (GUI) valutato ma scartato per peso e dipendenze grafiche.
*Alternativa di fallback:* modalità live + script `nas-install` interattivo da console.

### AD-6 — Layout storage: **OS su disco di sistema dedicato, dischi dati intatti** ✅ deciso
**Perché:** il partizionamento automatico tocca **solo** il device di sistema selezionato;
i dischi dati vengono gestiti dopo l'installazione tramite la GUI RAID (`mdadm`).
Previene la distruzione accidentale di array esistenti.

### AD-7 — Provisioning primo avvio: **servizio oneshot `nasd-firstboot`** ✅ deciso
**Perché:** al primo boot genera certificato TLS (self-signed iniziale), imposta hostname,
configura rete (DHCP default), e mostra in console l'URL + istruzioni per creare l'admin
via `nasctl`. Esperienza "accendi e vai".

### AD-8 — Sicurezza di rete: **nftables default-deny** ✅ deciso
**Perché:** espone solo `8443/tcp` (UI) e opzionalmente `22/tcp` (SSH key-only).
Le porte di share (NFS/SMB/DLNA) si aprono dinamicamente solo quando una share è attivata.

### AD-9 — Privilege separation di `nasd`: **helper privilegiato dedicato** 🔧 da confermare
**Opzioni:** (a) `nasd` non-root + helper setuid minimale per i soli comandi privilegiati;
(b) `nasd` root con hardening systemd stringente. Preferenza (a) per minimo privilegio.
Decisione da chiudere prima della Fase 5. Allineato a `ROADMAP.md` punto 9.
**Punto di applicazione — `system.Runner` / `system.ExecRunner`:** la privilege
separation va applicata esattamente al *chokepoint* di esecuzione comandi. Tutti i
domain manager (RAID `mdadm`, share `exportfs`/Samba, utenti `useradd`, DLNA) chiamano
l'interfaccia `system.Runner`, la cui unica implementazione concreta è `system.ExecRunner`
(già sede della validazione argomenti / allowlist anti-injection). Quindi l'helper
privilegiato deve mediare proprio le chiamate di `ExecRunner`: è lì che la decisione di
minimo privilegio diventa codice. Questa decisione è quindi il *rationale_for* di
`system.ExecRunner`.

### AD-10 — Asset frontend: **serviti localmente (no CDN)** ✅ deciso
**Perché:** un NAS può non avere accesso Internet; Preact/HTM vanno serviti da
`/assets/vendor`. Allineato a `ROADMAP.md` punto 11.

---

## 4. Stato attuale (baseline da `nasweb/`)

| Modulo | Backend | API | Frontend |
|---|:--:|:--:|:--:|
| Auth / sessioni | ✅ | ✅ | ✅ |
| i18n IT/EN | — | — | ✅ |
| Utenti / gruppi | ✅ | ✅ | ✅ |
| RAID (mdadm) | ✅ | ✅ | ✅ |
| S.M.A.R.T. | ✅ | ✅ | 🔧 |
| Share NFS/SMB | ✅ | 🔧 | 🔧 |
| File manager | ✅ | 🔧 | 🔧 |
| DLNA | ✅ | 🔧 | 🔧 |
| WebSocket eventi | ✅ | ✅ | ✅ |

La distro **non parte da zero**: il payload software è a ~60%. Il piano copre il
completamento del software *e* tutta la catena di distribuzione.

---

## 5. Fasi, milestone e deliverable

### Fase 1 — Completamento software NAS (MVP funzionale)
*Chiude le righe 🔧 della tabella sopra. Riferimento: `ROADMAP.md` priorità alta.*
- `system.Runner.RunStdin` (sblocca cambio password via `chpasswd`).
- Handler HTTP share (`/api/shares` CRUD + `ApplyNFS`/`ApplySMB` + conferme).
- Handler HTTP file manager (list/mkdir/rename/delete/chmod, upload multipart con
  evento `file.progress`, download `io.Copy`).
- Viste Preact: share, file manager (albero + drag&drop + progress da WebSocket), DLNA.
- **Milestone M1:** tutti i moduli ✅/✅/✅; demo end-to-end su VM.
- **Deliverable:** codice sorgente completo, tabella stato tutta verde.

### Fase 2 — Packaging (AD-3, AD-4, AD-10) — ✅ COMPLETATA
- ✅ Migrazione a `modernc.org/sqlite` (v1.34.4); build statica `CGO_ENABLED=0`
  verificata (`not a dynamic executable`), `go-sqlite3`/CGO rimosso.
- ✅ Pacchetto `.deb` (`scripts/build-deb.sh`): `nasd`, `nasctl`, `nasd-firstboot`,
  unit systemd in `/lib/systemd/system`, asset vendor locali; verificato con `dpkg-deb`.
- ✅ Asset frontend vendored (`scripts/vendor-frontend.sh` + import map): no CDN.
- ✅ `nasd-firstboot` (unit oneshot + script): TLS self-signed, dir dati, URL.
- ⏳ Repo APT locale **firmato** per l'ISO → spostato in Fase 3 (serve nel contesto build-iso).
- **Milestone M2 raggiunta:** `apt install ./nasd_*.deb` installa/abilita/avvia il servizio.
- **Deliverable:** `nasd_<ver>_amd64.deb`, `scripts/build.sh`, `scripts/build-deb.sh`,
  `scripts/vendor-frontend.sh`.

### Fase 3 — Build distribuzione / ISO (AD-1, AD-2, AD-5) — in corso
- ✅ Repo APT locale **firmato** (`scripts/build-apt-repo.sh`): `apt-ftparchive` +
  firma GPG; verificato (`InRelease` → Good signature). Era il pezzo rinviato dalla Fase 2.
- ✅ Config `live-build` (`scripts/build-iso.sh`): Debian stable minimal amd64, ISO
  ibrida + installer; pacchetti (nasd dal repo locale + mdadm/samba/nfs/minidlna/
  nftables…), branding, repo offline in `/opt/nasd-repo`, hook servizi. Validato `bash -n`.
- ✅ Preseed installer (`scripts/preseed.cfg`): partiziona **solo il disco di sistema** (AD-6).
- ⏳ `lb build` per produrre l'ISO: richiede host Debian con root + `live-build`/
  `debootstrap`/`xorriso` (non disponibili nell'ambiente di sviluppo attuale).
- **Milestone M3:** ISO ibrida che fa boot in live e installa — *da eseguire su build host*.
- **Deliverable:** `ollozunaos-amd64.iso` + checksum (prodotti su build host).

### Fase 4 — Installazione e provisioning (AD-6, AD-7)
- Preseed: partizionamento del solo disco di sistema, esclusione dischi dati.
- `nasd-firstboot`: TLS, hostname, rete DHCP, URL in console.
- Flusso creazione primo admin (`nasctl create-admin`).
- **Milestone M4:** da ISO a UI raggiungibile su `https://<ip>:8443` senza intervento manuale oltre il primo admin.
- **Deliverable:** installer riproducibile, runbook di provisioning.

### Fase 5 — Hardening e sicurezza (AD-8, AD-9)
- nftables default-deny + apertura dinamica porte share.
- Privilege separation (chiusura AD-9) e profili AppArmor per `nasd`.
- TLS reale (Let's Encrypt / CA interna), `CheckOrigin` stringente sul WebSocket.
- **Milestone M5:** scan di base superato; `nasd` non-root (o hardening equivalente).
- **Deliverable:** baseline di sicurezza documentata.

### Fase 6 — Test e QA (verifica vincoli §2)
- VM QEMU/KVM: 4 GB RAM, CPU emulata "vecchia generazione".
- Misura footprint a riposo (target < 512 MB), tempo di boot.
- Scenari RAID (create/degraded/rebuild), share NFS/SMB da client reale, upload grandi file.
- Test installazione pulita + upgrade pacchetto.
- **Milestone M6:** tutti i criteri di accettazione verificati.
- **Deliverable:** report di test, matrice scenari/esiti.

### Fase 7 — Documentazione e rilascio
- Guide installazione/configurazione/uso IT/EN.
- Note di rilascio, requisiti hardware, checksum/firma ISO.
- **Milestone M7 (Release v1.0):** ISO + doc pubblicabili.
- **Deliverable:** pacchetto di rilascio completo.

---

## 6. Work Breakdown Structure (sintesi)

```
1 Software NAS        → RunStdin · API share · API filemgr · viste Preact
2 Packaging           → modernc sqlite · build statica · .deb · repo APT
3 ISO                 → live-build config · pacchetti · branding · preseed
4 Install/provisioning→ partizionamento · firstboot · admin · rete
5 Hardening           → nftables · privsep · AppArmor · TLS · WS origin
6 QA                  → footprint · boot · RAID · share · upgrade
7 Docs/Release        → guide IT/EN · release notes · checksum
```

---

## 7. Rischi e mitigazioni

| Rischio | Impatto | Mitigazione |
|---|---|---|
| CGO/glibc complica build statica | Packaging fragile | AD-4: passare a modernc.org/sqlite (puro Go) |
| Footprint > 512 MB a riposo | Vincolo violato | Profilare in Fase 6; disattivare servizi non essenziali; lazy-start share |
| Installer distrugge dischi dati | Perdita dati utente | AD-6: partizionare solo il disco di sistema; conferme esplicite |
| Comandi privilegiati come root | Superficie d'attacco | AD-9: privilege separation + AppArmor |
| Hardware datato senza driver | Boot fallito | Kernel Debian stock con firmware non-free incluso nell'ISO |
| Operazioni RAID/share corrompono sistema | Affidabilità | Validazione (`testparm`/`exportfs`) e scritture atomiche già previste nel backend |

---

## 8. Criteri di accettazione v1.0

1. ISO installabile su VM con 4 GB RAM e CPU x86_64 datata.
2. Servizi `nasd` a riposo < ~512 MB RAM.
3. UI raggiungibile in HTTPS subito dopo installazione + creazione admin.
4. Tutte le funzionalità del prompt operative: utenti, share NFS/SMB, file manager,
   RAID, DLNA, i18n IT/EN.
5. Nessuna operazione critica può corrompere il sistema (conferme + validazione).
6. Documentazione IT/EN completa.

---

## 9. Tracciabilità con graphify

Questo piano è incorporato nel knowledge graph (`graphify-out/`). Ogni decisione `AD-*`
è un nodo con relazione `rationale_for` verso il componente che giustifica. A ogni
azione o decisione presa:

1. Si aggiorna questo documento (decisione + motivazione).
2. Si esegue `/graphify <path> --update` per riflettere la modifica nel grafo.
3. Le query (`/graphify query "..."`) interrogano lo stato aggiornato del progetto.

Stato decisioni: **AD-1..AD-8, AD-10 decise** · **AD-9 da confermare prima della Fase 5**.
