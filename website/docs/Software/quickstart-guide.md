---
title: Quick Start
sidebar_position: 1
---

# Quick Start Guide

Get WLEDger V2 up and running in minutes via Docker or a standalone Pure-Go binary.

## Option 1: Docker (Recommended)

The easiest way to run WLEDger is with Docker Compose. The container runs as a hardened non-root user (`wledger`, UID `10001`) with all web assets, translations, and SQLite migrations embedded inside the binary.

### 1. Create a Project Directory

Create folders for your persistent database and uploaded files:

```bash
mkdir -p wledger/data wledger/uploads && cd wledger
```

### 2. Create `docker-compose.yaml`

Create a file named `docker-compose.yaml`:

```yaml
services:
  wledger:
    image: tuxedomakes/wledger:latest
    container_name: wledger
    ports:
      - "8080:8080"
    # Optional: enable built-in self-signed HTTPS (needed for mobile camera scanning & clipboard over LAN)
    # environment:
    #   WLEDGER_TLS_SELF_SIGNED: "true"
    volumes:
      - ./data:/wledger/data
      - ./uploads:/wledger/app/uploads
    restart: unless-stopped
```

### 3. Start the Server

Run the container in the background:

```bash
docker compose up -d
```

---

## Option 2: Single-Binary Install (No Docker Required)

Because WLEDger compiles as a static **Pure-Go** binary (`CGO_ENABLED=0` with embedded assets and pure-Go SQLite), you can run it directly on Linux, macOS, or Windows with zero external runtime dependencies.

1. Download the archive for your OS/architecture (`linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64`, or `windows/amd64`) from [GitHub Releases](https://github.com/tuxedocurly/wledger/releases).
2. Extract and run:

```bash
tar -xzf wledger_v*_linux_amd64.tar.gz
./wledger
```

---

## Environment Variables & Built-In HTTPS

All configuration options are optional and have backward-compatible defaults:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `WLEDGER_PORT` | `8080` | HTTP/HTTPS listen port (or `host:port`) |
| `WLEDGER_DATA_DIR` | `./data` | Directory for the SQLite DB and TLS certificates |
| `WLEDGER_DB_PATH` | `<DataDir>/wledger.db` | Explicit SQLite database file path |
| `WLEDGER_UPLOADS_DIR` | `./app/uploads` | Directory for uploaded part images and datasheets |
| `WLEDGER_TLS_SELF_SIGNED` | `false` | Set `true` to auto-generate and persist an ECDSA P-256 LAN HTTPS certificate in `<DataDir>/tls` |
| `WLEDGER_TLS_CERT` / `WLEDGER_TLS_KEY` | *(empty)* | Paths to custom PEM certificate and private key |
| `WLEDGER_TRUST_PROXY` | `false` | Set `true` behind a reverse proxy to trust `X-Forwarded-For` / `X-Real-IP` headers |
| `WLEDGER_COOKIE_SECURE` | `false` | Set `true` when serving behind an HTTPS reverse proxy (auto-enabled when built-in TLS is active) |

:::tip Mobile Camera Scanning & Clipboard Over LAN
Modern mobile browsers block camera access (`getUserMedia`) and `navigator.clipboard` on plain `http://` LAN addresses. Set `WLEDGER_TLS_SELF_SIGNED=true` (or put WLEDger behind an HTTPS reverse proxy) and visit `https://<server-ip>:8080` to use your phone camera for 2D supplier barcode scanning!
:::

---

## Initial Setup Wizard

1. Open your browser and navigate to `http://localhost:8080` (or `https://localhost:8080` with TLS enabled).
2. Complete the **Setup Wizard** by creating your **Admin Account** (Email and Password, 8–72 characters).
3. You will be logged in and redirected to the Dashboard.

---

## Hardware & Storage Setup

WLEDger supports both WLED-controlled LED storage and controller-less **Standalone Containers**.

### Option A: Use WLEDger Without Any LEDs (Standalone Containers)

You do not need a WLED controller or a dummy IP to organize parts in WLEDger:

1. Go to **Hardware** and click **Add Standalone Container**.
2. Enter a container name (e.g., `Travel Case #1`) and grid dimensions (rows/cols).
3. Open **Configure** to customize bins or mount the container directly onto Dashboard Walls.
4. Later, if you add LEDs, you can bind any standalone container to a WLED controller directly from **Hardware**.

### Option B: Connect a WLED Controller

**Prerequisites:**

* A microcontroller running [WLED](https://kno.wled.ge/) (e.g., Sparkle Motion, Dig2Go, ESP32) with addressable LEDs (WS2812B/SK6812) on the same LAN.

1. In WLEDger, go to **Hardware**.
2. Click **Discover WLED** to scan your LAN automatically via mDNS (`_wled._tcp.local.`), **or** click **Add Controller** and enter the name, IP address, and port (default `80`).
3. Click **Configure** on the controller to open the **Grid Painter**:
    * Choose **Linear Strip**, **Matrix**, **Compound**, or **X/Y Crosshair (2 Strips)** layout.
    * Configure multi-LED wide bins (`Width >= 1`) if a physical bin spans multiple LEDs, or X/Y strip start indices and wiring directions for 2-strip crosshair setups.
    * Use **Auto Map (Linear)**, **Auto Map (Snake)**, or **Populate X/Y Bins** (or click bins manually), then click **Save Changes**.

### Organize with Walls (Optional)

1. Go to the **Dashboard** and click **Create Wall**.
2. Click **Edit Wall** to add any combination of WLED containers and Standalone Containers onto one view.

### Add Parts, Scan Barcodes & Locate

* **Add a Part:** Go to **Inventory** -> **Add Part**, or scan a **2D DigiKey / Mouser / LCSC** bag label with your phone camera or barcode scanner to auto-fill MPN, manufacturer, and supplier fields.
* **Bulk Import:** Click **Import** on **Inventory** to upload or paste a CSV.
* **Locate:** Click the **Locate** (eye) icon on any part card, part detail page, or inside a **Project**'s **Pick Mode** to light up every bin storing that component!

---

## Next Steps

* **[Hardware Build Guide](../Hardware/build-guide.md):** Advice on building LED storage cabinets and custom PCBs.
* **[Feature Guide](./feature-guide/inventory.md):** Detailed walkthrough of Inventory, Projects & BOM Pick Mode, WLED v2 features, and REST API v1.
* **[Developer Guide](./developer-guide.md):** Pure-Go architecture and local development guide.
