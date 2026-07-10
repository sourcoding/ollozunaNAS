#!/usr/bin/env bash
# Provisioning al primo avvio di nasd:
#  - prepara le directory dati,
#  - genera un certificato TLS self-signed se assente (sostituibile in seguito
#    con Let's Encrypt o una CA interna),
#  - stampa in console l'URL della UI e come creare il primo amministratore.
# Idempotente: se il certificato esiste già non viene rigenerato.
set -euo pipefail

TLS_DIR="/etc/nasd/tls"
CERT="$TLS_DIR/cert.pem"
KEY="$TLS_DIR/key.pem"

mkdir -p /var/lib/nasd /srv/nas "$TLS_DIR"
chmod 700 "$TLS_DIR"

if [[ ! -s "$CERT" || ! -s "$KEY" ]]; then
  echo "[nasd-firstboot] genero certificato TLS self-signed…"
  hostname_fqdn="$(hostname -f 2>/dev/null || hostname)"
  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "$KEY" -out "$CERT" -days 3650 \
    -subj "/CN=${hostname_fqdn}" \
    -addext "subjectAltName=DNS:${hostname_fqdn},DNS:localhost" >/dev/null 2>&1
  chmod 600 "$KEY"
fi

ip_addr="$(hostname -I 2>/dev/null | awk '{print $1}')"
ip_addr="${ip_addr:-<ip-del-nas>}"

cat <<EOF

  ┌──────────────────────────────────────────────────────────┐
  │  nasd è pronto.                                            │
  │  Interfaccia:  https://${ip_addr}:8443
  │                                                            │
  │  Crea il primo amministratore (una sola volta):           │
  │    sudo nasctl create-admin -u admin -p 'TUA_PASSWORD'     │
  └──────────────────────────────────────────────────────────┘

EOF
