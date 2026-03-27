# Windows (WSL2 + Docker Desktop) Setup Guide

This guide walks Windows developers through getting Docker Desktop running with
WSL2 so they can use `docker compose` to build and start AIISTECH-Backend.

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Enable Required Windows Features](#2-enable-required-windows-features)
3. [Verify CPU Virtualization](#3-verify-cpu-virtualization)
4. [Install / Verify Docker Desktop](#4-install--verify-docker-desktop)
5. [Verify Docker Connectivity](#5-verify-docker-connectivity)
6. [Run Compose](#6-run-compose)
7. [Recommended: Run from WSL Instead of Git Bash](#7-recommended-run-from-wsl-instead-of-git-bash)
8. [Troubleshooting](#8-troubleshooting)
9. [Safe Reset Steps](#9-safe-reset-steps)

---

## 1. Prerequisites

| Requirement | Notes |
|---|---|
| Windows 10 (21H2+) or Windows 11 | WSL2 requires a 64-bit build ≥ 1903 |
| WSL2 kernel installed | Run `wsl --update` to make sure it is current |
| Docker Desktop for Windows (latest) | [Download](https://www.docker.com/products/docker-desktop/) |

---

## 2. Enable Required Windows Features

Both of these features must be enabled **before** Docker Desktop can use the WSL2 backend.

1. Press **Win + R**, type `optionalfeatures`, press **Enter**.
2. Check both:
   - **Windows Subsystem for Linux**
   - **Virtual Machine Platform**
3. Click **OK**, allow Windows to install them, then **reboot**.

> **Tip:** You can also enable them from an elevated PowerShell:
> ```powershell
> dism.exe /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart
> dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart
> Restart-Computer
> ```

---

## 3. Verify CPU Virtualization

WSL2 requires hardware virtualization (Intel VT-x or AMD-V) to be enabled in
your BIOS/UEFI firmware.

**Check from PowerShell (no reboot needed):**
```powershell
systeminfo | findstr /i "Virtualization Hyper-V"
```

Look for:
- `Virtualization Enabled In Firmware: Yes` — you are good to go.
- `Virtualization Enabled In Firmware: No` — enter your BIOS/UEFI settings and
  enable **Intel VT-x** / **AMD-V** / **SVM Mode**, then reboot.

> Consult your motherboard or laptop manufacturer's manual for BIOS key and
> setting location (common keys: F2, Del, F10, Esc during POST).

---

## 4. Install / Verify Docker Desktop

1. Download and install [Docker Desktop for Windows](https://www.docker.com/products/docker-desktop/).
2. During setup, when asked about the backend, select **"Use WSL 2 instead of Hyper-V"**.
3. Open Docker Desktop and wait until the status bar shows **"Engine running"**.
4. Confirm Docker Desktop provisioned its WSL distros:

```powershell
wsl -l -v
```

Expected output (STATE may be `Running` or `Stopped`):
```
  NAME                   STATE           VERSION
* Ubuntu-24.04           Running         2
  docker-desktop         Running         2
  docker-desktop-data    Stopped         2
```

> **If `docker-desktop` and `docker-desktop-data` are missing**, Docker Desktop
> did not finish provisioning. See [Troubleshooting](#8-troubleshooting).

---

## 5. Verify Docker Connectivity

From **PowerShell** or **Command Prompt** (not Git Bash for this check):

```powershell
docker version
docker info
```

Both commands must succeed (no error messages). A healthy `docker info` output
ends with lines like:
```
Server Version: 27.x.x
...
```

---

## 6. Run Compose

You **must** run `docker compose` from the repository root (the directory that
contains `docker-compose.yml`).

### Git Bash
```bash
cd ~/AIISTECH-Backend          # or wherever you cloned the repo
cp .env.example .env           # first time only; edit .env with your values
bash scripts/dev.sh doctor     # optional pre-flight check
bash scripts/dev.sh up
```

### PowerShell
```powershell
cd C:\Users\<you>\AIISTECH-Backend   # adjust path as needed
Copy-Item .env.example .env          # first time only
.\scripts\dev.ps1 doctor             # optional pre-flight check
.\scripts\dev.ps1 up
```

### Plain docker compose (any shell, from repo root)
```bash
docker compose up -d --build
```

> **Common mistake:** running `docker compose up` from your home directory (`~`
> or `C:\Users\<you>`) produces:
> ```
> no configuration file provided: not found
> ```
> Always `cd` into the repo root first, or pass `-f`:
> ```bash
> docker compose -f ~/AIISTECH-Backend/docker-compose.yml up -d --build
> ```

---

## 7. Recommended: Run from WSL Instead of Git Bash

Git Bash (MINGW64) translates Windows paths and can occasionally have
compatibility issues with Docker's named-pipe socket. Running from a WSL
terminal is more reliable.

1. Open **Ubuntu** (or whichever WSL distro you use) from the Start menu.
2. Navigate to the repo — if you cloned into `C:\Users\<you>\AIISTECH-Backend`,
   the WSL path is:
   ```bash
   cd /mnt/c/Users/<you>/AIISTECH-Backend
   ```
   Or clone directly inside WSL for the best I/O performance:
   ```bash
   cd ~
   git clone https://github.com/RRussell11/AIISTECH-Backend.git
   cd AIISTECH-Backend
   ```
3. Run compose as normal:
   ```bash
   cp .env.example .env
   docker compose up -d --build
   ```

---

## 8. Troubleshooting

### `open //./pipe/dockerDesktopLinuxEngine: The system cannot find the file specified.`

Docker Desktop's engine is not running (or is still starting up).

1. Open Docker Desktop and wait for **"Engine running"** (can take 1–3 min).
2. If it stays on "Starting…", try: **Troubleshoot → Restart Docker Desktop**.
3. Check that `docker-desktop` appears in `wsl -l -v`. If not, see
   [Safe Reset Steps](#9-safe-reset-steps).

---

### `no configuration file provided: not found`

You are not in the repository root. Fix:

```bash
cd ~/AIISTECH-Backend
docker compose up -d --build
```

Or from anywhere:
```bash
docker compose -f ~/AIISTECH-Backend/docker-compose.yml up -d --build
```

---

### `docker-desktop` / `docker-desktop-data` missing from `wsl -l -v`

Docker Desktop never finished provisioning its WSL distros. Try in order:

**Option A — Restart Docker Desktop**
1. Right-click the Docker Desktop tray icon → **Restart**.
2. Wait 2–5 minutes, then check `wsl -l -v` again.

**Option B — Reset Docker Desktop WSL integration** (see [Safe Reset Steps](#9-safe-reset-steps))

**Option C — Reinstall Docker Desktop**
1. Uninstall Docker Desktop from **Settings → Apps → Installed apps**.
2. Reboot.
3. Download and reinstall the latest Docker Desktop.
4. Start it and wait for **"Engine running"**.
5. Verify `wsl -l -v` shows `docker-desktop` and `docker-desktop-data`.

---

### Docker Desktop gets stuck on "Starting the Docker Engine…"

- Confirm **Virtual Machine Platform** is enabled (Step 2).
- Confirm CPU virtualization is enabled in BIOS (Step 3).
- Try running `wsl --shutdown` in PowerShell, then restart Docker Desktop.
- As a last resort, do a clean reinstall (Option C above).

---

## 9. Safe Reset Steps

> ⚠️ **Warning — data loss:** unregistering the `docker-desktop-data` distro
> **permanently deletes all Docker images, containers, volumes, and build
> cache** stored in that distro. Make sure you have pushed any important images
> to a registry before proceeding.

### Reset Docker Desktop WSL distros

Open **PowerShell as Administrator**:

```powershell
# 1. Shut down all WSL distros
wsl --shutdown

# 2. Remove Docker Desktop's distros
#    This deletes all local images and container data.
wsl --unregister docker-desktop
wsl --unregister docker-desktop-data

# 3. Restart Docker Desktop (from the Start menu or taskbar)
#    Wait for "Engine running" — Docker Desktop will recreate the distros.
```

After Docker Desktop starts successfully, verify:
```powershell
wsl -l -v          # should show docker-desktop and docker-desktop-data
docker info        # should print engine details
```

### Reset to factory defaults (GUI)

1. Open Docker Desktop.
2. Click **Troubleshoot** (bug icon, top-right).
3. Click **Reset to factory defaults**.

> ⚠️ This also removes all images, containers, and volumes — the same
> destructive effect as unregistering the distros above.
