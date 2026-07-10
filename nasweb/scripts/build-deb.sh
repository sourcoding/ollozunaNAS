#!/usr/bin/env bash
# Costruisce il pacchetto Debian nasd_<versione>_amd64.deb a partire da ./dist
# (prodotto da scripts/build.sh). Richiede dpkg-deb e fakeroot.
#
#   scripts/build.sh && scripts/build-deb.sh
set -euo pipefail

VERSION="${VERSION:-0.1.0}"
ARCH="amd64"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="$ROOT/dist"

if [[ ! -d "$DIST" ]]; then
  echo "Esegui prima scripts/build.sh per produrre ./dist" >&2
  exit 1
fi

# dpkg-deb pretende permessi <=0775 sulla dir DEBIAN. Su filesystem montati da
# Windows (drvfs) tutto è 777 e chmod non attecchisce, quindi si fa lo staging
# in una directory Linux-native (TMPDIR) e si copia il .deb finale nel repo.
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
PKG="$STAGE/nasd_${VERSION}_${ARCH}"

rm -rf "$ROOT/deb-build"
mkdir -p "$ROOT/deb-build" "$PKG"
cp -r "$DIST"/* "$PKG"/

# Normalizza i permessi: il filesystem sorgente (drvfs/Windows) forza tutto a
# 777. Dati 0644, directory 0755, solo i binari/eseguibili 0755.
find "$PKG" -type d -exec chmod 0755 {} +
find "$PKG" -type f -exec chmod 0644 {} +
chmod 0755 "$PKG/usr/bin/nasd" "$PKG/usr/bin/nasctl" "$PKG/usr/bin/nasd-firstboot"

mkdir -p "$PKG/DEBIAN"

# Dimensione installata (KiB) per il campo Installed-Size.
INSTALLED_SIZE="$(du -ks "$PKG" | cut -f1)"

cat > "$PKG/DEBIAN/control" <<EOF
Package: nasd
Version: ${VERSION}
Section: admin
Priority: optional
Architecture: ${ARCH}
Maintainer: nasweb <sandro.ginnari@accenture.com>
Installed-Size: ${INSTALLED_SIZE}
Depends: openssl
Recommends: mdadm, samba, nfs-kernel-server, smartmontools, minidlna
Description: NAS Web Management Interface (lightweight DSM alternative)
 Interfaccia web leggera per gestire un NAS Linux: utenti, share NFS/SMB,
 file manager, RAID software (mdadm), DLNA e diagnostica S.M.A.R.T.
 Backend Go statico (CGO-free), frontend Preact senza bundler.
EOF

# config.yaml è un file di configurazione: non sovrascriverlo agli aggiornamenti.
cat > "$PKG/DEBIAN/conffiles" <<EOF
/etc/nasd/config.yaml
EOF

cat > "$PKG/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
mkdir -p /var/lib/nasd /srv/nas
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
  systemctl enable nasd-firstboot.service nasd.service >/dev/null 2>&1 || true
  # Avvia firstboot (genera TLS e stampa l'URL) e il daemon.
  systemctl start nasd-firstboot.service || true
  systemctl start nasd.service || true
fi
exit 0
EOF

cat > "$PKG/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = "remove" ] && [ -d /run/systemd/system ]; then
  systemctl stop nasd.service || true
  systemctl disable nasd.service nasd-firstboot.service >/dev/null 2>&1 || true
fi
exit 0
EOF

cat > "$PKG/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
# I dati (/var/lib/nasd, /srv/nas) NON vengono rimossi nemmeno al purge,
# per non distruggere configurazione e contenuti dell'utente.
exit 0
EOF

# Permessi richiesti da dpkg-deb: DEBIAN 0755, maintainer scripts 0755.
chmod 0755 "$PKG/DEBIAN"
chmod 0755 "$PKG/DEBIAN/postinst" "$PKG/DEBIAN/prerm" "$PKG/DEBIAN/postrm"
chmod 0644 "$PKG/DEBIAN/control" "$PKG/DEBIAN/conffiles"

OUT="$ROOT/deb-build/nasd_${VERSION}_${ARCH}.deb"
fakeroot dpkg-deb --build --root-owner-group "$PKG" "$STAGE/out.deb"
cp "$STAGE/out.deb" "$OUT"
echo "Pacchetto creato: $OUT"
