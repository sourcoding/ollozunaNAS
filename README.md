# ollozunaNAS

**A lightweight, self-hosted NAS operating system with a modern web interface — a lean alternative to Synology DSM for x86 hardware (including older machines).**

ollozunaNAS ships as an installable Debian-based ISO. It boots, installs itself, and gives you a web UI to manage RAID, network shares, users, media streaming and torrents — all from the browser. The backend is a single static Go binary; the frontend is a Preact SPA with no build step. It runs comfortably on 4 GB of RAM.

---

## Features

- **RAID management (mdadm)** — create/manage RAID 0/1/5/6/10 and linear arrays, add/remove disks, hot spares, rebuild monitoring, S.M.A.R.T. info, disk wipe.
- **Filesystems** — create/format ext4 / btrfs / xfs, mount/unmount, grow, automatic mount on boot. Missing `mkfs` tools are installed on demand.
- **Network shares** — SMB/CIFS (Samba) and NFS, with per-share advanced options (guest, recycle bin, Time Machine, SMB encryption, NFS per-client rules & squashing). Enable/disable without restarts; configs validated before applying.
- **File manager** — browse, upload/download, rename, delete, create folders, chmod, with a folder picker on every path field.
- **DLNA media server** (MiniDLNA) — share media folders, browse the library by category, rescan.
- **qBittorrent** — installable in-app with a guided setup wizard, dedicated WebUI port, and a shared-media permission model.
- **Administration** — reboot / shutdown / live system logs from the UI.
- **Bilingual** — full Italian / English interface.
- **Secure by design** — HTTPS, HttpOnly+Secure session cookies, CSRF protection, strict input validation, least-privilege services.

---

## Download

Grab the latest installer ISO from the **[Releases page](https://github.com/sourcoding/ollozunaNAS/releases/latest)** (`ollozunaOS-<version>.iso`).

---

## Installation

### Requirements

- **CPU**: x86_64 (Intel/AMD), including legacy CPUs.
- **RAM**: 4 GB minimum.
- **OS disk**: a small disk or a USB stick (≈ 8 GB+) to install the system onto.
- **Data disks**: one or more additional disks for storage / RAID (optional but recommended).

### 1. Write the ISO to a USB stick

Use any USB imaging tool:

- **Linux/macOS** (replace `/dev/sdX` with your USB device — double-check it!):
  ```bash
  sudo dd if=ollozunaOS-<version>.iso of=/dev/sdX bs=4M status=progress oflag=sync
  ```
- **Cross-platform GUI**: [balenaEtcher](https://etcher.balena.io/).
- **Windows**: [Rufus](https://rufus.ie/) (DD mode).
- **Ventoy**: copy the `.iso` onto a Ventoy USB and select it at boot.

### 2. Boot and install

1. Boot the target machine from the USB stick (adjust BIOS/UEFI boot order).
2. The boot menu defaults to **Install** and starts automatically after **5 seconds**.
3. The installation is automated (preseed). It will pause only to ask you:
   - **which disk** to install the OS onto (pick your small OS disk / USB target);
   - a **password** for the system user `nasadmin`.

> ⚠️ **Destructive step — read this.** Before partitioning, the installer **wipes RAID signatures and partition tables from all fixed disks** to avoid orphaned RAID arrays that would stall the install. **The boot/installer medium is always protected** (USB devices — including an SSD on a USB‑to‑SATA adapter with Ventoy — and the disk hosting the install media are excluded). Back up anything important on the internal disks before installing.

4. When it finishes, remove the USB stick and let the machine reboot.

### 3. First boot & create the admin

On first boot the system generates a self-signed TLS certificate and prints, on the console, the UI address and how to create the first administrator.

1. Log in at the console (or via SSH) as **`nasadmin`** with the password you set during installation:
   ```bash
   ssh nasadmin@<nas-ip>
   ```
2. Create the web-interface administrator (one time):
   ```bash
   sudo nasctl create-admin -u admin -p 'YOUR_STRONG_PASSWORD'
   ```
3. Open the web UI in your browser:
   ```
   https://<nas-ip>:8443
   ```
   (The IP is shown in the console banner; accept the self-signed certificate warning.) Log in as **admin** with the password you just set.

---

## First steps in the web UI

1. **RAID** → select disks and create an array (the OS disk is hidden automatically).
2. **RAID → Create Filesystem** → format the array (ext4/btrfs/xfs) and mount it (e.g. `/srv/nas/md0`).
3. **Shares** → create SMB and/or NFS shares on the mounted volume.
4. **DLNA** → add media folders and rescan to stream to your TV/players.
5. **qBittorrent** → configure the app (a data volume must exist first).
6. **Administration** → view logs, reboot or shut down.

---

## Build from source

Requirements: Go, `live-build`, and standard build tools on a Debian/Ubuntu host.

```bash
cd nasweb
./scripts/build.sh          # build the Go backend + assemble the frontend/config
./scripts/build-deb.sh      # package nasd as a .deb
./scripts/build-apt-repo.sh # index a local APT repo
sudo ./scripts/build-iso.sh # build the installable ISO (needs root)
```

The ISO is produced in `nasweb/iso-build/` as `ollozunaOS-<version>.iso`; the version auto-increments on each build (`nasweb/ISO_VERSION`).

To iterate on a running system you can hot-deploy the `nasd` binary, web assets and migrations without rebuilding the ISO.

---

## Architecture

```
Browser ──HTTPS/WSS──► nasd (Go static binary) ──► REST API + WebSocket
                        │
                        ├─ system runner (validated exec): mdadm, smartctl,
                        │  samba/exportfs, useradd, minidlna, qbittorrent-nox
                        └─ SQLite (config, app users, shares)
Frontend: Preact + HTM SPA (no bundler), served by nasd. Bilingual IT/EN.
```

- **Minimal footprint**: static Go binary, framework-less SPA, SQLite.
- **Safety**: explicit confirmation for destructive operations, config validated (`testparm`, `exportfs`) before applying, atomic config writes, generated (never string-concatenated) configuration.

See [`nasweb/README.md`](nasweb/README.md) for the developer-oriented documentation and [`nasweb/docs/FIX-LOG.md`](nasweb/docs/FIX-LOG.md) for the detailed change log.

---

## Default network & services

- Web UI: `https://<ip>:8443`
- Hostname: `ollozunaos`
- System user: `nasadmin` (sudo; root login disabled)
- SSH: enabled (OpenSSH)
- Samba/NFS/MiniDLNA are enabled on demand by the interface, not by default.
