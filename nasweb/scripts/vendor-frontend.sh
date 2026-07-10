#!/usr/bin/env bash
# Scarica Preact, preact/hooks e HTM da esm.sh e li salva come file ESM
# autoconsistenti in <dest>/assets/vendor, così la UI funziona senza CDN
# (vincolo: il NAS può essere offline). hooks.mjs importa "preact" come bare
# specifier: l'import map in index.html lo risolve sul preact.mjs vendored,
# preservando un'unica istanza di Preact.
#
# Uso:  scripts/vendor-frontend.sh <dest-www-dir>
#       (default: <repo>/frontend/public)
set -euo pipefail

PREACT_VER="10.19.3"
HTM_VER="3.1.1"
BASE="https://esm.sh"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="${1:-$ROOT/frontend/public}"
VENDOR="$DEST/assets/vendor"
mkdir -p "$VENDOR"

# Risolve lo stub esm.sh (export * from "/deep/path") e scarica il bundle reale.
fetch_module() {
  local url="$1" out="$2"
  local stub deep
  stub="$(curl -fsSL "$url")"
  deep="$(printf '%s' "$stub" | grep -oE 'from ?"[^"]+"' | head -1 | sed -E 's/from ?"([^"]+)"/\1/')"
  if [[ -z "$deep" ]]; then
    # Nessun re-export: lo stub è già il modulo.
    printf '%s' "$stub" > "$out"
  else
    curl -fsSL "${BASE}${deep}" -o "$out"
  fi
  # Sanity check: file non vuoto.
  [[ -s "$out" ]] || { echo "vendoring fallito per $url" >&2; exit 1; }
}

echo "==> Vendoring frontend in $VENDOR"
fetch_module "${BASE}/preact@${PREACT_VER}?bundle&target=es2022"                 "$VENDOR/preact.mjs"
# hooks con preact esterno -> importa "preact" come bare (risolto dall'import map)
fetch_module "${BASE}/preact@${PREACT_VER}/hooks?external=preact&target=es2022"  "$VENDOR/hooks.mjs"
fetch_module "${BASE}/htm@${HTM_VER}?bundle&target=es2022"                        "$VENDOR/htm.mjs"

echo "Vendored:"
ls -l "$VENDOR"
