#!/bin/sh
# scrub-disks.sh — scrub preventivo dei dischi PRIMA del partizionamento.
# Invocato dall'installer via preseed `partman/early_command` (il file viene
# incluso nell'initrd dell'installer come /scrub-disks.sh).
#
# Scopo: rendere "vergini" i dischi FISSI azzerando i superblock mdadm e le firme
# (in testa e in coda al device), così l'installer non riassembla array RAID
# orfani (tipicamente /dev/md127) che bloccano/rallentano grub-installer.
#
# NON deve MAI toccare il supporto di installazione / il disco di boot. Vengono
# quindi PROTETTI (esclusi dallo scrub):
#   1) tutti i dischi collegati via USB — incluso un SSD SATA su convertitore
#      USB->SATA con Ventoy a bordo (il boot device dell'utente);
#   2) il disco che ospita il medium di installazione, risolto attraverso loop e
#      device-mapper (es. /dev/mapper/ventoy) e partizioni, fino al disco fisico.
#
# È deliberatamente conservativo: nel dubbio NON cancella (meglio lasciare un
# disco sporco che distruggere il supporto di boot). Gli eventuali dischi extra
# si possono comunque ripulire dalla UI dopo l'installazione.
set +e

log() { echo "scrub-disks: $*"; }

# Ferma eventuali array già assemblati (orfani inclusi), altrimenti i device
# risultano occupati e non azzerabili.
mdadm --stop --scan 2>/dev/null

protected=""
add_prot()  { protected="$protected $1 "; }
is_prot()   { case " $protected " in *" $1 "*) return 0 ;; esac; return 1; }

# parent_disk <kernel-name> -> nome del disco padre.
#   sda1 -> sda ; nvme0n1p2 -> nvme0n1 ; sda -> sda ; (loopN -> "")
parent_disk() {
  pn="$1"
  if [ -f "/sys/class/block/$pn/partition" ]; then
    d=$(readlink -f "/sys/class/block/$pn/..")
    echo "${d##*/}"
    return
  fi
  case "$pn" in
    sd*|vd*|nvme*) [ -e "/sys/block/$pn" ] && echo "$pn" ;;
  esac
}

# 1) Protezione: tutti i dischi sul bus USB.
for b in /sys/block/sd* /sys/block/vd* /sys/block/nvme*; do
  [ -e "$b" ] || continue
  n=${b##*/}
  case "$(readlink -f "$b")" in
    *"/usb"*) add_prot "$n"; log "protetto (USB): $n" ;;
  esac
done

# 2) Protezione: disco che ospita il medium di installazione. Segue eventuali
#    loop/device-mapper (Ventoy) tramite gli "slaves" fino a sd/vd/nvme.
resolve_medium() {
  src="$1"
  [ -b "$src" ] || return
  kn=$(readlink -f "$src"); kn=${kn##*/}
  tries=0
  while [ "$tries" -lt 8 ]; do
    tries=$((tries + 1))
    case "$kn" in
      sd*|vd*|nvme*)
        pd=$(parent_disk "$kn")
        [ -n "$pd" ] && { add_prot "$pd"; log "protetto (medium): $pd"; }
        return ;;
    esac
    # loop/dm: scendi al primo slave (device sottostante)
    sl=$(ls "/sys/class/block/$kn/slaves" 2>/dev/null | head -n1)
    [ -n "$sl" ] || return
    kn="$sl"
  done
}
for mp in /cdrom /run/live/medium /lib/live/mount/medium /run/live/persistence; do
  s=$(awk -v m="$mp" '$2==m{print $1; exit}' /proc/mounts 2>/dev/null)
  [ -n "$s" ] && resolve_medium "$s"
done

# 3) Scrub dei soli dischi NON protetti.
for b in /sys/block/sd* /sys/block/vd* /sys/block/nvme*; do
  [ -e "$b" ] || continue
  n=${b##*/}
  d="/dev/$n"
  [ -b "$d" ] || continue
  if is_prot "$n"; then
    log "salto $d (protetto)"
    continue
  fi
  log "azzero $d"
  for p in "$d"*; do
    [ -b "$p" ] && mdadm --zero-superblock --force "$p" 2>/dev/null
  done
  # testa: MBR/GPT primario + superblock mdadm 1.1/1.2
  dd if=/dev/zero of="$d" bs=1M count=16 2>/dev/null
  # coda: GPT di backup + superblock mdadm 1.0 (ultimi ~16 MiB)
  sz=$(cat "/sys/block/$n/size" 2>/dev/null)
  [ -n "$sz" ] && [ "$sz" -gt 40960 ] && \
    dd if=/dev/zero of="$d" bs=512 seek=$((sz - 32768)) count=32768 2>/dev/null
done

true
