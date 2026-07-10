# Build host — produrre l'ISO di ollozunaOS

Istruzioni per costruire l'immagine ISO installabile su una macchina di build
dedicata. La build dell'ISO **richiede root** e tool che creano un chroot
Debian: va eseguita su un host Debian/Ubuntu, non sull'ambiente di sviluppo.

> Sintesi pipeline: `build.sh` → `build-deb.sh` → `build-apt-repo.sh` → `build-iso.sh`.

---

## 1. Requisiti del build host

- **OS**: Debian 12/13 (consigliato) o Ubuntu 22.04+. live-build produce immagini
  *Debian*; usare un host Debian riduce le sorprese.
- **Privilegi**: `root` (o `sudo`). `lb build` monta filesystem e usa loop device.
- **Risorse**: ~10 GB liberi su disco, rete verso un mirror Debian.
- **Pacchetti host**:

```bash
sudo apt-get update
sudo apt-get install -y \
  live-build debootstrap xorriso \
  dpkg-dev apt-utils gnupg \
  golang-go ca-certificates curl
```

Verifica della toolchain Go (serve **≥ 1.22**; nessun gcc/CGO richiesto):

```bash
go version   # go1.22 o superiore
```

Se la versione nei repo è troppo vecchia, installare Go ufficiale:

```bash
curl -fsSL https://go.dev/dl/go1.22.12.linux-amd64.tar.gz -o /tmp/go.tgz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf /tmp/go.tgz
export PATH=/usr/local/go/bin:$PATH
```

---

## 2. Ottenere il sorgente

Copiare la cartella `nasweb/` sul build host (git clone o copia diretta). Tutti i
comandi seguenti si eseguono dalla **radice di `nasweb/`**.

```bash
cd nasweb
```

> Gli asset frontend (Preact/HTM) sono già vendored in
> `frontend/public/assets/vendor`. Per rinfrescarli (richiede rete):
> `./scripts/vendor-frontend.sh`.

---

## 3. Build dei binari e degli asset

```bash
cd backend && go mod download && cd ..
./scripts/build.sh        # binari statici (CGO-free) + asset -> ./dist
```

Controllo opzionale che il binario sia statico:

```bash
file dist/usr/bin/nasd     # atteso: "statically linked / not a dynamic executable"
```

---

## 4. Pacchetto .deb

```bash
./scripts/build-deb.sh     # -> deb-build/nasd_0.1.0_amd64.deb
dpkg-deb --info     deb-build/nasd_0.1.0_amd64.deb   # metadati
dpkg-deb --contents deb-build/nasd_0.1.0_amd64.deb   # file inclusi
```

Per cambiare versione: `VERSION=0.2.0 ./scripts/build-deb.sh`.

---

## 5. Repo APT locale (consigliato: firmato)

### 5a. (Una tantum) creare una chiave GPG di firma del repo

```bash
gpg --batch --gen-key <<'EOF'
%no-protection
Key-Type: RSA
Key-Length: 4096
Key-Usage: sign
Name-Real: ollozunaOS repo
Name-Email: repo@ollozunaos.local
Expire-Date: 0
%commit
EOF
# Recupera l'ID chiave:
gpg --list-secret-keys --with-colons | awk -F: '/^sec/{print $5; exit}'
```

### 5b. Costruire il repo

```bash
GPG_KEY_ID=<ID_CHIAVE> ./scripts/build-apt-repo.sh   # -> ./apt-repo (firmato)
# Senza GPG_KEY_ID il repo è non firmato e l'ISO lo usa con [trusted=yes].
gpg --verify apt-repo/InRelease    # atteso: "Good signature"
```

---

## 6. Build dell'ISO

```bash
sudo ./scripts/build-iso.sh
# Output: iso-build/live-image-amd64.hybrid.iso
```

Tempo: diversi minuti (scarica il base system dal mirror). Al termine, checksum:

```bash
sha256sum iso-build/live-image-amd64.hybrid.iso > iso-build/SHA256SUMS
```

Cosa fa l'ISO: installa `nasd` dal repo locale insieme ai servizi NAS
(mdadm/samba/nfs/minidlna/nftables), include il repo in `/opt/nasd-repo` per
upgrade offline, abilita `nasd-firstboot` + `nasd`, e usa `scripts/preseed.cfg`
per partizionare **solo il disco di sistema**.

> ⚠️ **Disco di sistema**: `preseed.cfg` punta a `/dev/sda`. Su hardware con NVMe
> (`/dev/nvme0n1`) o ordini di disco diversi, adattare `partman-auto/disk` e
> `grub-installer/bootdev` prima della build. I dischi NON elencati restano intatti.

---

## 7. Test in VM (prima di scrivere su hardware)

```bash
sudo apt-get install -y qemu-system-x86 ovmf
qemu-system-x86_64 -m 4096 -smp 2 -enable-kvm \
  -drive file=iso-build/live-image-amd64.hybrid.iso,format=raw,if=virtio,readonly=on \
  -drive file=/tmp/nas-os.img,format=qcow2,if=virtio \
  -boot d -net nic -net user,hostfwd=tcp::8443-:8443
```

(Creare prima il disco di sistema virtuale: `qemu-img create -f qcow2 /tmp/nas-os.img 16G`.)
Verifica il vincolo di footprint: a riposo i servizi nasd devono stare **< ~512 MB**.

---

## 8. Scrittura su USB e primo avvio

```bash
sudo dd if=iso-build/live-image-amd64.hybrid.iso of=/dev/sdX bs=4M status=progress oflag=sync
```

Al primo boot `nasd-firstboot` genera il certificato TLS e stampa l'URL. Poi:

```bash
sudo nasctl create-admin -u admin -p 'SCEGLI_UNA_PASSWORD'
# UI: https://<ip-del-nas>:8443
```

---

## 9. Risoluzione problemi

- **`lb build` fallisce su mount/loop**: esegui come root e non dentro un container
  senza privilegi; serve accesso a `/dev/loop*` e `mount`.
- **`dpkg-deb: control directory has bad permissions 777`**: stai costruendo su un
  filesystem montato da Windows (drvfs). `build-deb.sh` fa già lo staging in
  `TMPDIR`; assicurati che `TMPDIR` sia su un filesystem Linux nativo.
- **Pacchetti NAS mancanti nell'ISO**: verifica che `apt-recommends` sia attivo e
  che la rete del build host raggiunga il mirror Debian.
- **Driver SQLite/CGO**: il backend usa `modernc.org/sqlite` (puro Go); non serve
  gcc. Se `go build` chiede CGO, controlla `CGO_ENABLED=0` (impostato in `build.sh`).
