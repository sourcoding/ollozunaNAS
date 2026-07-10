#!/usr/bin/env bash
# Costruisce un'ISO Debian minimal installabile con nasd preinstallato (via .deb).
# Usa live-build. DA ESEGUIRE su Debian/Ubuntu con root e i pacchetti:
#   sudo apt-get install live-build
#   ./scripts/build.sh && ./scripts/build-deb.sh && ./scripts/build-apt-repo.sh
#   sudo ./scripts/build-iso.sh
#
# Questo script prepara la configurazione live-build e lancia `lb build`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$ROOT/iso-build"
DEB="$(ls -1 "$ROOT/deb-build"/nasd_*_amd64.deb 2>/dev/null | head -1 || true)"
REPO="$ROOT/apt-repo"

# --- Versione ISO: usa la corrente per QUESTA build, poi la incrementa (patch)
# per la PROSSIMA. Il nome del file ISO include la versione (ollozunaOS-X.Y.Z.iso).
VERSION_FILE="$ROOT/ISO_VERSION"
[[ -f "$VERSION_FILE" ]] || echo "0.1.0" > "$VERSION_FILE"
VERSION="$(tr -d ' \t\n\r' < "$VERSION_FILE")"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || VERSION="0.1.0"
ISO_NAME="ollozunaOS-${VERSION}.iso"

if [[ -z "$DEB" ]]; then
  echo "Manca il .deb: esegui scripts/build.sh && scripts/build-deb.sh" >&2
  exit 1
fi

# Il repo APT locale deve esistere ed essere indicizzato, altrimenti l'APT del
# chroot fallisce molto più avanti con "file:/opt/nasd-repo/Packages not found".
# Meglio fermarsi subito con un messaggio chiaro (era mascherato dal `|| true`).
if [[ ! -f "$REPO/Packages" || ! -f "$REPO/Release" ]]; then
  echo "Repo APT locale mancante o non indicizzato ($REPO/Packages assente):" >&2
  echo "  esegui prima scripts/build-apt-repo.sh" >&2
  exit 1
fi

rm -rf "$WORK"
mkdir -p "$WORK"
cd "$WORK"

# --- Configurazione live-build: Debian stable minimal, amd64, ISO ibrida ---
# --debian-installer live: include l'installer (per installare su disco).
lb config \
  --distribution stable \
  --architectures amd64 \
  --binary-images iso-hybrid \
  --debian-installer live \
  --debian-installer-gui false \
  --archive-areas "main contrib non-free non-free-firmware" \
  --memtest none \
  --apt-recommends true

# --- Pacchetti installati nel sistema (servizi orchestrati da nasd) ---
mkdir -p config/package-lists
cat > config/package-lists/nas.list.chroot <<'EOF'
nasd
mdadm
samba
smbclient
cifs-utils
nfs-kernel-server
nfs-common
smartmontools
minidlna
qbittorrent-nox
acl
nftables
openssl
util-linux
e2fsprogs
xfsprogs
btrfs-progs
sudo
ca-certificates
openssh-server
EOF

# --- nasd .deb: copiato in config/packages.chroot/ → live-build lo installa
#     direttamente senza bisogno di una sorgente APT separata. ---
mkdir -p config/packages.chroot
cp "$DEB" config/packages.chroot/

# --- Repo incluso nel sistema installato (per reinstall/upgrade offline) ---
mkdir -p config/includes.chroot/opt/nasd-repo
cp "$REPO"/*.deb "$REPO"/Packages* "$REPO"/Release config/includes.chroot/opt/nasd-repo/
[[ -f "$REPO/InRelease" ]] && cp "$REPO/InRelease" config/includes.chroot/opt/nasd-repo/ || true
mkdir -p config/includes.chroot/etc/apt/sources.list.d
cat > config/includes.chroot/etc/apt/sources.list.d/nasd.list <<'EOF'
deb [trusted=yes] file:/opt/nasd-repo ./
EOF

# --- Branding minimale ---
mkdir -p config/includes.chroot/etc
echo "ollozunaos" > config/includes.chroot/etc/hostname
cat > config/includes.chroot/etc/motd <<'EOF'

  ollozunaOS — NAS Web Management Interface
  UI: https://<ip>:8443   ·   crea admin: sudo nasctl create-admin -u admin -p '...'

EOF

# --- Hook: abilita i servizi nasd, disabilita di default quelli orchestrati ---
mkdir -p config/hooks/live
cat > config/hooks/live/0100-nasd.hook.chroot <<'EOF'
#!/bin/sh
set -e
mkdir -p /var/lib/nasd /srv/nas
systemctl enable nasd-firstboot.service nasd.service || true
# I servizi di rete (samba/nfs/minidlna) li abilita nasd su richiesta.
systemctl disable smbd nmbd nfs-server minidlna 2>/dev/null || true
# Samba deve includere le share generate da nasd, altrimenti non le serve.
touch /etc/samba/nasd-shares.conf
if ! grep -q 'nasd-shares.conf' /etc/samba/smb.conf 2>/dev/null; then
  printf '\n   include = /etc/samba/nasd-shares.conf\n' >> /etc/samba/smb.conf
fi
# Abilita il fallback guest per le share pubbliche (senza valid users).
if ! grep -q 'map to guest' /etc/samba/smb.conf 2>/dev/null; then
  sed -i '/^\[global\]/a\   map to guest = Bad User' /etc/samba/smb.conf
fi
# File exports iniziale (nasd lo rigenera a ogni modifica).
touch /etc/exports
# Disabilita os-prober anche nel sistema installato: senza questo, ogni
# update-grub (es. dopo un aggiornamento kernel) tornerebbe a scandire tutti i
# dischi dati, reintroducendo la lentezza. NAS = single-boot, non serve.
if [ -f /etc/default/grub ]; then
  if grep -q '^GRUB_DISABLE_OS_PROBER=' /etc/default/grub; then
    sed -i 's/^GRUB_DISABLE_OS_PROBER=.*/GRUB_DISABLE_OS_PROBER=true/' /etc/default/grub
  else
    echo 'GRUB_DISABLE_OS_PROBER=true' >> /etc/default/grub
  fi
fi
EOF
chmod +x config/hooks/live/0100-nasd.hook.chroot

# --- Preseed per l'installer (partiziona SOLO il disco di sistema; AD-6) ---
# preseed.cfg e scrub-disks.sh finiscono nella root dell'initrd dell'installer:
# il preseed richiama `sh /scrub-disks.sh` in partman/early_command.
mkdir -p config/includes.installer
cp "$ROOT/scripts/preseed.cfg" config/includes.installer/preseed.cfg
cp "$ROOT/scripts/scrub-disks.sh" config/includes.installer/scrub-disks.sh
chmod +x config/includes.installer/scrub-disks.sh

echo "==> Avvio build ISO v${VERSION} (richiede root e diversi minuti)"
lb build

# Rinomina l'output con la versione. Rimuoviamo il nome non versionato così
# run-vm.sh (fallback: ISO più recente in iso-build/) prende sempre l'ultima.
HYBRID="$WORK/live-image-amd64.hybrid.iso"
if [[ -f "$HYBRID" ]]; then
  mv -f "$HYBRID" "$WORK/$ISO_NAME"
fi

# Incrementa la patch per la PROSSIMA build (solo dopo un build riuscito).
NEXT="$(echo "$VERSION" | awk -F. '{printf "%d.%d.%d", $1, $2, $3+1}')"
echo "$NEXT" > "$VERSION_FILE"

echo "ISO generata: $WORK/$ISO_NAME  (prossima build: v${NEXT})"
