# VM di test — ollozunaOS

Ambiente QEMU/KVM per testare l'ISO installabile senza scrivere su hardware.

## Dischi (dir `disks/`)
- `system.qcow2` (16G) — candidato disco OS (virtio-scsi)
- `data1/2/3.qcow2` (4G) — dischi dati (storage/RAID, virtio-scsi)
- `usb.qcow2` (8G) — **chiavetta USB** dedicata all'OS (via xHCI); creata al volo
  se manca, disattivabile con `USB=0`

Il disco su cui va l'OS si **sceglie durante l'installazione** (voce *Start
installer*): l'installer chiede quale disco partizionare e dove mettere GRUB,
lasciando intatti gli altri. Così puoi mettere l'OS **sulla chiavetta USB** e
usare i dischi solo per lo spazio dati. In `run` la VM avvia dalla USB se ci
trova l'OS, altrimenti da `system.qcow2` (ordine via bootindex).

Nella lista dischi dell'installer la chiavetta **non è etichettata "USB"**:
riconoscila dalla **dimensione 8 GB** (i dischi dati sono 4 GB, `system` 16 GB).
Seleziona lo stesso disco anche per GRUB.

## Prerequisiti (una tantum, nel TUO terminale con sudo)
    sudo usermod -aG kvm box    # poi: logout/login (o `newgrp kvm`)

## Uso
    ./run-vm.sh install         # installazione: trova la ISO da solo in ../nasweb/iso-build/
    ./run-vm.sh install <ISO>   # installazione da una ISO specifica
    ./run-vm.sh run             # boot dal sistema installato su system.qcow2
    ./run-vm.sh -h              # aiuto

Lo script è a prova di errore: se manca il comando, la ISO o i dischi, o se provi
`run` senza aver ancora installato nulla, esce con un messaggio chiaro invece di
avviare in silenzio una VM che non fa il boot.

**Dopo l'installazione:** chiudi la finestra e avvia con `./run-vm.sh run`. In
modalità `install` il CD ha priorità di boot, quindi al riavvio ripartirebbe la
ISO invece del sistema appena installato.

UI NAS dall'host: https://localhost:8443  (SSH: `ssh -p 2222 user@localhost`)

## Rete
Due modalità, scelte con la variabile `NET`:

- `NET=bridge` (default) — la VM è un host sulla LAN, prende un IP dal router.
  La UI è su `https://<ip-della-VM>:8443` (l'IP compare nel banner al login).
- `NET=user` — NAT dietro l'host; UI su `https://localhost:8443`, SSH `-p 2222`.
  Esempio: `NET=user ./run-vm.sh run`

Se il bridge non è pronto, lo script avvisa e ripiega automaticamente su `user`.

### Setup bridge (una tantum, nel TUO terminale con sudo — solo su rete cablata)
Abilita l'helper QEMU per il bridge `br0`:

    sudo install -d /etc/qemu
    echo "allow br0" | sudo tee -a /etc/qemu/bridge.conf
    sudo chmod u+s /usr/lib/qemu/qemu-bridge-helper

Crea il bridge `br0` che ingloba la NIC `enp1s0` (via NetworkManager):

    sudo nmcli connection add type bridge   con-name br0      ifname br0 stp no
    sudo nmcli connection add type ethernet con-name br0-port ifname enp1s0 master br0
    sudo nmcli connection up br0

`br0` prende l'IP in DHCP; se l'host usa IP statico, replicalo sul bridge:

    sudo nmcli con mod br0 ipv4.method manual \
      ipv4.addresses 192.168.0.111/24 ipv4.gateway 192.168.0.1 ipv4.dns 192.168.0.1

Per tornare indietro:

    sudo nmcli con down br0 && sudo nmcli con del br0 br0-port && sudo nmcli con up enp1s0

Bridge diverso o MAC/NIC diversi: `BRIDGE=mybr0 MAC=52:54:00:xx:xx:xx ./run-vm.sh run`.

## Reset dischi
    rm -f disks/*.qcow2
    qemu-img create -f qcow2 disks/system.qcow2 16G
    for i in 1 2 3; do qemu-img create -f qcow2 disks/data$i.qcow2 4G; done
    qemu-img create -f qcow2 disks/usb.qcow2 8G   # chiavetta USB (o lascia che la crei run-vm.sh)
