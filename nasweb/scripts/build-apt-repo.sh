#!/usr/bin/env bash
# Costruisce un repository APT locale "flat" contenente il pacchetto nasd, da
# includere nell'ISO (live-build) così che nasd si installi via apt con le sue
# dipendenze. Genera l'indice Packages(.gz) e un file Release; firma il Release
# con GPG se è disponibile una chiave (GPG_KEY_ID), altrimenti lascia il repo
# non firmato (l'ISO lo userà con [trusted=yes]).
#
#   scripts/build.sh && scripts/build-deb.sh && scripts/build-apt-repo.sh
#   GPG_KEY_ID=ABCD1234 scripts/build-apt-repo.sh   # con firma
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEB_DIR="$ROOT/deb-build"
REPO="$ROOT/apt-repo"

DEB="$(ls -1 "$DEB_DIR"/nasd_*_amd64.deb 2>/dev/null | head -1 || true)"
if [[ -z "$DEB" ]]; then
  echo "Nessun .deb in $DEB_DIR — esegui prima scripts/build.sh && scripts/build-deb.sh" >&2
  exit 1
fi

rm -rf "$REPO"
mkdir -p "$REPO"
cp "$DEB" "$REPO/"

cd "$REPO"
echo "==> Indice Packages"
dpkg-scanpackages --multiversion . /dev/null > Packages
gzip -9c Packages > Packages.gz

echo "==> Release"
apt-ftparchive \
  -o APT::FTPArchive::Release::Origin=nasweb \
  -o APT::FTPArchive::Release::Label=nasweb \
  -o APT::FTPArchive::Release::Suite=stable \
  -o APT::FTPArchive::Release::Codename=nasd \
  -o APT::FTPArchive::Release::Architectures=amd64 \
  -o APT::FTPArchive::Release::Components=main \
  release . > Release

if [[ -n "${GPG_KEY_ID:-}" ]]; then
  echo "==> Firma Release con chiave $GPG_KEY_ID"
  rm -f Release.gpg InRelease
  gpg --default-key "$GPG_KEY_ID" --batch --yes -abs -o Release.gpg Release
  gpg --default-key "$GPG_KEY_ID" --batch --yes --clearsign -o InRelease Release
  echo "Repo firmato."
else
  echo "ATTENZIONE: GPG_KEY_ID non impostato → repo NON firmato (usare [trusted=yes] nell'ISO)."
fi

echo "Repo APT pronto in $REPO"
ls -1 "$REPO"
