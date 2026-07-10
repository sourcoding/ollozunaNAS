#!/usr/bin/env bash
# Avvia la VM di test per ollozunaOS con QEMU/KVM.
#
# Layout dischi (virtio-scsi, ordine = naming Linux):
#   system.qcow2 -> /dev/sd?   (candidato disco OS)
#   data1..3     -> /dev/sd?   (dischi dati per storage/RAID)
#   usb.qcow2    -> chiavetta USB (via xHCI, se USB=1)  -> candidato disco OS
# Il disco su cui installare l'OS si SCEGLIE durante l'installazione (vedi
# preseed.cfg): p.es. l'OS sulla chiavetta USB e i dischi liberi per lo storage.
# In 'run' si avvia dal disco che contiene l'OS (USB se presente, altrimenti
# system.qcow2) tramite bootindex.
#
# Uso:
#   ./run-vm.sh install [ISO]   # boot da ISO (se ISO omessa, la cerca in nasweb/iso-build/)
#   ./run-vm.sh run             # boot dal sistema installato (USB o system.qcow2)
#   ./run-vm.sh -h              # aiuto
#
# Variabili d'ambiente:
#   NET=bridge|user (default bridge)   BRIDGE=br0   MAC=52:54:00:a1:10:2a
#   USB=1|0 (default 1)   USB_SIZE=8G   GUI=gtk|none|sdl (default gtk)
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DISKS="$DIR/disks"
ISO_DIR="$(cd "$DIR/.." && pwd)/nasweb/iso-build"
DEFAULT_ISO="$ISO_DIR/live-image-amd64.hybrid.iso"

# Rete: NET=bridge (VM come host sulla LAN) | NET=user (NAT + port forward).
NET="${NET:-bridge}"
BRIDGE="${BRIDGE:-br0}"
MAC="${MAC:-52:54:00:a1:10:2a}"   # MAC fisso -> lease DHCP stabile

# Chiavetta USB dedicata all'OS (per lo scenario "OS su USB, dischi = storage").
USB="${USB:-1}"
USB_SIZE="${USB_SIZE:-8G}"

# Display QEMU (gtk per la finestra; none per headless/CI).
GUI="${GUI:-gtk}"

usage() {
  cat >&2 <<EOF
Uso: $(basename "$0") <install|run> [ISO]

  install [ISO]  Avvia dalla ISO per installare l'OS sul disco SCELTO durante
                 l'installazione. Se ISO e' omessa, viene cercata in:
                   $ISO_DIR/
  run            Avvia dal disco che contiene l'OS (USB o system.qcow2).

Variabili: NET=bridge|user  BRIDGE=br0  USB=1|0  USB_SIZE=8G  GUI=gtk|none

Esempi:
  ./run-vm.sh install
  ./run-vm.sh install /percorso/mia.iso
  USB=0 ./run-vm.sh run
EOF
}

MODE="${1:-}"
ISO="${2:-}"

case "$MODE" in
  -h|--help|help) usage; exit 0 ;;
  install|run) ;;
  "")  echo "ERRORE: manca il comando (install|run)." >&2; echo >&2; usage; exit 2 ;;
  *)   echo "ERRORE: comando sconosciuto: '$MODE'." >&2; echo >&2; usage; exit 2 ;;
esac

# --- Controlli preliminari ---------------------------------------------------

# qemu presente?
command -v qemu-system-x86_64 >/dev/null 2>&1 || {
  echo "ERRORE: qemu-system-x86_64 non trovato. Installa QEMU (es. 'sudo apt install qemu-system-x86')." >&2
  exit 3
}

# Dischi dati/sistema presenti? (la chiavetta USB la creiamo noi al volo)
missing=()
for d in system data1 data2 data3; do
  [[ -f "$DISKS/$d.qcow2" ]] || missing+=("$d.qcow2")
done
if ((${#missing[@]})); then
  echo "ERRORE: dischi mancanti in $DISKS/: ${missing[*]}" >&2
  echo "        Creali con:" >&2
  echo "          qemu-img create -f qcow2 $DISKS/system.qcow2 16G" >&2
  echo "          for i in 1 2 3; do qemu-img create -f qcow2 $DISKS/data\$i.qcow2 4G; done" >&2
  exit 4
fi

# Chiavetta USB dedicata: creala se manca (e' un target OS scratch, sicuro).
if [[ "$USB" == "1" && ! -f "$DISKS/usb.qcow2" ]]; then
  echo "==> Creo chiavetta USB dedicata: $DISKS/usb.qcow2 ($USB_SIZE)"
  qemu-img create -f qcow2 "$DISKS/usb.qcow2" "$USB_SIZE" >/dev/null
fi

# Un disco "sembra installato" se il file qcow2 e' cresciuto oltre la soglia
# (un qcow2 appena creato pesa ~200 KiB). Vale sia per system.qcow2 che per la USB.
INSTALL_THRESHOLD=$((20 * 1024 * 1024))   # 20 MiB
disk_has_os() {
  local sz; sz=$(stat -c%s "$1" 2>/dev/null || echo 0)
  (( sz > INSTALL_THRESHOLD ))
}
os_installed() {
  disk_has_os "$DISKS/system.qcow2" && return 0
  [[ "$USB" == "1" ]] && disk_has_os "$DISKS/usb.qcow2" && return 0
  return 1
}

# Boot via bootindex (piu' robusto di -boot c/d su q35 con piu' dischi + USB).
CD_ARGS=()
SYS_BOOTIDX=""   # bootindex del disco system.qcow2 (vuoto = non avviabile)
USB_BOOTIDX=""   # bootindex della chiavetta USB
if [[ "$MODE" == "install" ]]; then
  # Auto-rilevamento ISO se non passata come argomento
  if [[ -z "$ISO" ]]; then
    if [[ -f "$DEFAULT_ISO" ]]; then
      ISO="$DEFAULT_ISO"
    else
      ISO="$(ls -1t "$ISO_DIR"/*.iso 2>/dev/null | head -n1 || true)"
    fi
    [[ -n "$ISO" ]] && echo "==> ISO auto-rilevata: $ISO"
  fi
  if [[ -z "$ISO" ]]; then
    echo "ERRORE: nessuna ISO indicata e nessuna trovata in $ISO_DIR/." >&2
    echo "        Indica il percorso: ./run-vm.sh install /percorso/mia.iso" >&2
    exit 5
  fi
  if [[ ! -f "$ISO" ]]; then
    echo "ERRORE: ISO non trovata: '$ISO'" >&2
    exit 5
  fi
  if command -v file >/dev/null 2>&1 && ! file -L "$ISO" | grep -q "ISO 9660"; then
    echo "ATTENZIONE: '$ISO' non sembra una ISO 9660 (potrebbe non fare il boot)." >&2
  fi
  if os_installed; then
    echo "ATTENZIONE: un disco OS (system.qcow2 o usb.qcow2) sembra gia' installato." >&2
    echo "            Reinstallando lo sovrascrivi se lo riselezioni. Vedi 'Reset dischi' nel README." >&2
  fi
  # La ISO fa il boot per prima (bootindex 0). I dischi restano come target.
  CD_ARGS=(-drive "file=$ISO,format=raw,if=none,id=cd0,media=cdrom"
           -device "scsi-cd,drive=cd0,bus=scsi0.0,bootindex=0")
  echo "==> Boot INSTALLER da: $ISO"
else # run
  if ! os_installed; then
    echo "ERRORE: nessun sistema installato (system.qcow2 e usb.qcow2 vuoti)." >&2
    echo "        Esegui prima l'installazione:  ./run-vm.sh install" >&2
    exit 6
  fi
  # Avvia dal disco che CONTIENE l'OS (bootindex=0). Un disco VUOTO messo nel
  # boot order blocca SeaBIOS (che salta a rete/floppy -> "No bootable device"),
  # quindi teniamo nel boot order solo il disco giusto.
  if [[ "$USB" == "1" ]] && disk_has_os "$DISKS/usb.qcow2"; then
    USB_BOOTIDX=0; SYS_BOOTIDX=1   # OS sulla chiavetta USB (system come fallback)
    echo "==> Boot dal sistema installato sulla chiavetta USB"
  else
    SYS_BOOTIDX=0                  # OS su system.qcow2; la USB resta fuori dal boot order
    echo "==> Boot dal sistema installato su system.qcow2"
  fi
fi

# Accelerazione KVM se disponibile e accessibile
ACCEL=(-machine q35 -cpu host -enable-kvm)
if [[ ! -r /dev/kvm || ! -w /dev/kvm ]]; then
  echo "ATTENZIONE: /dev/kvm non accessibile (sei nel gruppo kvm?). Fallback a TCG (lento)." >&2
  ACCEL=(-machine q35 -cpu qemu64)
fi

# Controller virtio-scsi + dischi (system + data). bootindex solo dove serve.
DISK_ARGS=(-device virtio-scsi-pci,id=scsi0)
add_disk() { # $1=file $2=id [$3=bootindex]
  local extra=""
  [[ -n "${3:-}" ]] && extra=",bootindex=$3"
  DISK_ARGS+=(-drive "file=$1,format=qcow2,if=none,id=$2"
              -device "scsi-hd,drive=$2,bus=scsi0.0$extra")
}
add_disk "$DISKS/system.qcow2" sysdisk "$SYS_BOOTIDX"
add_disk "$DISKS/data1.qcow2"  data1
add_disk "$DISKS/data2.qcow2"  data2
add_disk "$DISKS/data3.qcow2"  data3

# Chiavetta USB: controller xHCI + usb-storage (removable => appare come USB).
USB_ARGS=()
if [[ "$USB" == "1" ]]; then
  ub=""; [[ -n "$USB_BOOTIDX" ]] && ub=",bootindex=$USB_BOOTIDX"
  USB_ARGS=(-device qemu-xhci,id=xhci
            -drive "file=$DISKS/usb.qcow2,format=qcow2,if=none,id=usbstick"
            -device "usb-storage,bus=xhci.0,drive=usbstick,removable=on,serial=OLLOZUNA-USB$ub")
  echo "==> Chiavetta USB collegata: $DISKS/usb.qcow2 (candidato disco OS)"
fi

# --- Rete ---------------------------------------------------------------------
# In bridge servono: il bridge $BRIDGE esistente e qemu-bridge-helper abilitato
# (allow nel bridge.conf + setuid). Se manca qualcosa, spiega e ripiega su user.
bridge_ready() {
  local helper reason=""
  ip link show "$BRIDGE" &>/dev/null || { reason="bridge '$BRIDGE' inesistente"; }
  if [[ -z "$reason" ]]; then
    grep -qsE "^\s*allow\s+$BRIDGE\s*$" /etc/qemu/bridge.conf || reason="'$BRIDGE' non in /etc/qemu/bridge.conf"
  fi
  if [[ -z "$reason" ]]; then
    helper=$(command -v qemu-bridge-helper || echo /usr/lib/qemu/qemu-bridge-helper)
    [[ $EUID -eq 0 || -u "$helper" ]] || reason="qemu-bridge-helper non setuid (ne' root)"
  fi
  [[ -z "$reason" ]] && return 0
  BRIDGE_REASON="$reason"; return 1
}

NET_ARGS=()
if [[ "$NET" == "bridge" ]] && bridge_ready; then
  NET_ARGS=(-netdev "bridge,id=net0,br=$BRIDGE" -device "virtio-net-pci,netdev=net0,mac=$MAC")
  echo "==> Rete: BRIDGE su $BRIDGE (la VM prende un IP dalla LAN)"
  echo "==> UI NAS: https://<ip-della-VM>:8443  (l'IP appare nel banner al login)"
else
  if [[ "$NET" == "bridge" ]]; then
    echo "ATTENZIONE: bridge non pronto (${BRIDGE_REASON:-?}). Ripiego su user-mode (NAT)." >&2
    echo "            Setup bridge una tantum: vedi README (sezione Rete bridged)." >&2
  fi
  NET_ARGS=(-netdev user,id=net0,hostfwd=tcp::8443-:8443,hostfwd=tcp::2222-:22
            -device "virtio-net-pci,netdev=net0,mac=$MAC")
  echo "==> Rete: USER/NAT  |  UI NAS: https://localhost:8443  |  SSH: ssh -p 2222 user@localhost"
fi

exec qemu-system-x86_64 \
  "${ACCEL[@]}" \
  -m 4096 -smp 2 \
  "${DISK_ARGS[@]}" \
  "${CD_ARGS[@]}" \
  "${USB_ARGS[@]}" \
  "${NET_ARGS[@]}" \
  -vga virtio -display "$GUI" \
  -name "ollozunaOS-test"
