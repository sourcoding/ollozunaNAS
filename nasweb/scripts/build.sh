#!/usr/bin/env bash
# Build del backend (binari Go statici, CGO-free) e raccolta degli asset del
# frontend in ./dist, pronto per il packaging .deb (scripts/build-deb.sh) o
# l'inclusione nell'ISO (scripts/build-iso.sh).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="$ROOT/dist"
rm -rf "$DIST"
mkdir -p "$DIST/usr/bin" \
         "$DIST/usr/share/nasd/www/assets/js" \
         "$DIST/usr/share/nasd/www/assets/css" \
         "$DIST/usr/share/nasd/www/assets/vendor" \
         "$DIST/usr/share/nasd/migrations" \
         "$DIST/lib/systemd/system" \
         "$DIST/etc/nasd"

echo "==> Build backend (Go, binari statici CGO-free)"
cd "$ROOT/backend"
# modernc.org/sqlite è puro Go: CGO_ENABLED=0 produce binari completamente
# statici (nessuna dipendenza glibc), ideali per packaging e ISO minimale.
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -ldflags="-s -w" -o "$DIST/usr/bin/nasd"   ./cmd/nasd
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -ldflags="-s -w" -o "$DIST/usr/bin/nasctl" ./cmd/nasctl
install -m 0755 "$ROOT/scripts/nasd-firstboot.sh" "$DIST/usr/bin/nasd-firstboot"

echo "==> Migrazioni"
cp "$ROOT"/backend/migrations/*.sql "$DIST/usr/share/nasd/migrations/"

echo "==> Frontend"
cp "$ROOT"/frontend/public/index.html "$DIST/usr/share/nasd/www/index.html"
cp "$ROOT"/frontend/src/js/*.js        "$DIST/usr/share/nasd/www/assets/js/"
cp -r "$ROOT"/frontend/src/i18n        "$DIST/usr/share/nasd/www/assets/"
cp "$ROOT"/frontend/src/css/*.css      "$DIST/usr/share/nasd/www/assets/css/"
# Immagini (logo NAS)
if compgen -G "$ROOT/frontend/public/assets/img/*" > /dev/null; then
  mkdir -p "$DIST/usr/share/nasd/www/assets/img"
  cp "$ROOT"/frontend/public/assets/img/* "$DIST/usr/share/nasd/www/assets/img/"
fi
# Asset vendored (Preact/HTM serviti localmente, no CDN). Se mancano, eseguire
# scripts/vendor-frontend.sh per scaricarli.
if compgen -G "$ROOT/frontend/public/assets/vendor/*.mjs" > /dev/null; then
  cp "$ROOT"/frontend/public/assets/vendor/*.mjs "$DIST/usr/share/nasd/www/assets/vendor/"
else
  echo "ATTENZIONE: asset vendor mancanti, esegui scripts/vendor-frontend.sh" >&2
fi

echo "==> Config e unit systemd"
cp "$ROOT/backend/config.example.yaml" "$DIST/etc/nasd/config.yaml"
cp "$ROOT/scripts/nasd.service"           "$DIST/lib/systemd/system/"
cp "$ROOT/scripts/nasd-firstboot.service" "$DIST/lib/systemd/system/"

echo "Build completata in $DIST"
