<p align="center">
  <img src="website/static/img/wledger-logo.png" alt="WLEDger Logo" width="500">
</p>
<div align="center">

[![Report Bug](https://img.shields.io/badge/GitHub-Report%20Bug-black?style=for-the-badge&logo=github)](https://github.com/tuxedocurly/wledger/issues) [![Join Discord](https://img.shields.io/badge/Discord-Get%20Support-5865F2?style=for-the-badge&logo=discord&logoColor=white)](https://discord.gg/wmDdcar7Kq) [![Support WLEDger](https://img.shields.io/badge/Ko--Fi-Support%20Me-FF5E5B?style=for-the-badge&logo=ko-fi&logoColor=white)](https://ko-fi.com/tuxedomakes)

![](https://img.shields.io/badge/built%20with%20ai%20assistance-gemini-brightgreen?style=for-the-badge "hover!")

</div>

> **The Ultimate Inventory Management System for Makers.**
> *Organize your electronic components and find them instantly with WLED.*

Check out the official WLEDger documentation site at [https://wledger.app](https://wledger.app).

## What is WLEDger?

WLEDger (WLED + Ledger) is a modern, high-performance inventory system designed specifically for electronics hobbyists, makers, and labs. It solves the problem of "I know I have this part, but where is it?" by integrating with **WLED** controllers.

When you search for a component, WLEDger doesn't just tell you "Bin A1" — **it lights up the specific bin on your storage rack.**

<details>
<summary><strong>Why I Made WLEDger</strong></summary>
I have hundreds of electronic components in my lab. I couldn't remember what parts I had or where they were stored, leading to over-buying and under-utilizing. I wanted a system that solved these problems and got me back to doing what I love: making!
<br><br>
Existing tools were expensive, closed source, or lacking in features and WLED integration. WLEDger bridges these gaps, providing a purpose-built, open-source inventory manager that's simple to use and feature-rich.
</details>

---

## Core Features

### Visual LED Locating System (WLED v2)

Connect your WLED-powered LED strips or matrices to your storage — or manage controller-less storage racks natively.

- **Visual Locate & Multi-LED Bins:** Click "Locate" on a part and its bin glows instantly, supporting multi-LED wide bins (`Width >= 1`).
- **Standalone Containers:** Create and organize controller-less containers without any fake controller IP workarounds, mount them on Dashboard Walls, and attach/detach them to WLED controllers anytime.
- **mDNS Auto-Discovery:** Discover `_wled._tcp.local.` devices on your LAN automatically, add them in one click, and self-heal DHCP IP changes matched by MAC address.
- **Controller Editing & State Restoration:** Configure HTTP ports, per-controller **UDP Realtime (DNRGB)** streaming, pre-locate WLED state snapshots with automatic restoration, or fall back to an **Idle Preset ID** when locating finishes.
- **Stock-Health Locate Colors:** Optionally illuminate located bins using stock status colors (`OK`, `Low`, `Critical`) alongside custom Walls / Dashboards.

### Powerful Inventory & 2D Supplier Barcode Scanning

- **Fast Search:** Instant results using pure-Go SQLite **FTS5** (Full-Text Search) with safe query sanitization for technical part strings like `ESP32-S3`, `0.1uF`, or `10k/1%`.
- **2D Supplier Bag Scanning:** Scan or paste **DigiKey** and **Mouser** ANSI MH10.8.2 / TIA-1063 DataMatrix/PDF417 labels, **LCSC** QR/DataMatrix labels, and 1D barcodes (EAN/UPC/Code128) to auto-fill MPN, Manufacturer, Supplier, and detect existing parts.
- **Built-In Mobile Camera Scanner:** Uses native `BarcodeDetector` with `html5-qrcode` fallback. Enable built-in self-signed HTTPS (`WLEDGER_TLS_SELF_SIGNED=true`) so mobile devices on your LAN have camera & clipboard access out of the box.
- **Rich Data & Tagging:** Store datasheets (PDFs), thumbnails, external links, cost data, and flexible tags.

### Projects & BOM Kitting ("Pick Mode")

- **KiCad & CSV BOM Import:** Upload or paste a BOM and auto-link rows to inventory by MPN, barcode, or part name.
- **Multi-Board Shortage Tracking:** Calculate component availability and shortages for `N` boards at a glance.
- **Pick Mode (`Locate All`):** Light up every bin needed for a project simultaneously across all WLED controllers, or locate individual BOM lines.
- **Atomic Project Builds:** Verify full BOM stock in a single transaction, deduct inventory across bins, and record full audit logs.

### REST API v1 & Personal Access Tokens

- **Scoped API Tokens:** Create `wlg_...` personal access tokens (`read`, `write`, `admin` scopes with optional expiry) in Settings, stored as SHA-256 hashes at rest.
- **JSON REST API (`/api/v1/*`):** Automate inventory queries, low-stock checks, stock adjustments, 2D barcode parsing, WLED locating, and project builds using `Authorization: Bearer wlg_...`.

### LLM Prompt Templates, Security & Backups

- **Inspiration Prompts:** Generate structured prompts populated with your live inventory for Gemini, ChatGPT, Claude, or local LLMs.
- **Hardened Auth & RBAC:** Admin, Editor, Viewer, and optional Guest roles, CSRF protection, login rate limiting, protected document/image uploads, and full audit logging.
- **Portable Backups & Bulk Import:** Export/restore full ZIP backups and bulk-import parts via CSV (including standalone containers).

### Localization

WLEDger supports 8 languages out of the box (embedded directly into the binary):

- English (`en`)
- German / Deutsch (`de`)
- Spanish / Español (`es`)
- French / Français (`fr`)
- Italian / Italiano (`it`)
- Portuguese / Português - Brasil (`pt-BR`)
- Russian / Русский (`ru`)
- Simplified Chinese / 简体中文 (`zh`)

> Non-English translations may contain inaccuracies. If you notice any translation errors or have suggestions for improvements, please [file an issue on GitHub](https://github.com/tuxedocurly/wledger/issues).

---

## The Tech Stack

WLEDger V2 is built as a self-contained, single-binary application with zero external runtime dependencies:

- **Backend:** [Go](https://go.dev/) (1.25+) — Pure-Go static compilation (`CGO_ENABLED=0`), embedded assets, locales, and migrations.
- **Database:** Pure-Go [SQLite](https://www.sqlite.org/) (`modernc.org/sqlite` with FTS5) + `sqlc` + `goose`.
- **Frontend:** [Templ](https://templ.guide/) + [HTMX](https://htmx.org/) + [Alpine.js](https://alpinejs.dev/) (all vendor JS bundled offline).
- **Styling:** [Tailwind CSS v4](https://tailwindcss.com/) + [DaisyUI v5](https://daisyui.com/).
- **Hardware:** [WLED](https://kno.wled.ge/) JSON API + UDP Realtime DNRGB + mDNS auto-discovery.

---

## Install WLEDger

### Option 1: Docker (Recommended)

The easiest way to run WLEDger is via Docker. The container runs as a hardened non-root user (`wledger`) with all static assets, locales, and database migrations embedded inside the binary.

1. Create a directory for your WLEDger data:

```bash
mkdir -p wledger/data wledger/uploads && cd wledger
```

2. Create `docker-compose.yaml`:

```yaml
services:
  wledger:
    image: tuxedomakes/wledger:latest
    container_name: wledger
    ports:
      - "8080:8080"
    # Optional: enable built-in HTTPS for mobile camera scanning & clipboard over LAN
    # environment:
    #   WLEDGER_TLS_SELF_SIGNED: "true"
    volumes:
      - ./data:/wledger/data
      - ./uploads:/wledger/app/uploads
    restart: unless-stopped
```

3. Start the container:

```bash
docker compose up -d
```

Visit `http://localhost:8080` (or `https://<host-ip>:8080` if `WLEDGER_TLS_SELF_SIGNED=true` is enabled) to complete the Setup Wizard.

> **mDNS Discovery in Docker:** On Linux hosts, set `network_mode: host` (and remove `ports:`) in [docker-compose.yaml](docker-compose.yaml) so WLEDger can send/receive LAN mDNS (`_wled._tcp.local.`) and resolve `.local` hostnames. On macOS/Windows, Docker containers run inside a Linux VM whose NAT bridge does not forward multicast, so add controllers by LAN IP address (or run the native binary for mDNS auto-discovery).

### Option 2: Pre-Built Pure-Go Single Binary

Download the static archive for your platform (`linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64`, or `windows/amd64`) from [GitHub Releases](https://github.com/tuxedocurly/wledger/releases):

```bash
tar -xzf wledger_v*_linux_amd64.tar.gz
./wledger
```

No Node.js, CGO, or SQLite shared libraries are required at runtime. Configure paths, port, TLS, or reverse-proxy behavior via environment variables (`WLEDGER_PORT`, `WLEDGER_DATA_DIR`, `WLEDGER_UPLOADS_DIR`, `WLEDGER_TLS_SELF_SIGNED`, `WLEDGER_TLS_CERT`, `WLEDGER_TLS_KEY`, `WLEDGER_TRUST_PROXY`, `WLEDGER_COOKIE_SECURE`).

### Option 3: Build from Source

**Prerequisites:**

- **Go:** Version 1.25+ (no C compiler/CGO needed for production builds)
- **Node.js:** Version 22+ (for Tailwind CSS compilation)
- **Make:** A `make` compatible command line tool

```bash
# 1. Clone the repository
git clone https://github.com/tuxedocurly/wledger.git
cd wledger

# 2. Install Go codegen tools (templ, sqlc) and Node dependencies
make install_dependencies

# 3. Generate templ, sqlc, and minified Tailwind CSS
make generate

# 4. Build a static pure-Go binary (CGO_ENABLED=0) or start the dev server
make build   # outputs ./bin/wledger
make dev     # live reload server + Tailwind watcher
```

---

## UI Screenshots

| Dashboard | Inventory |
| :---: | :---: |
| ![Dashboard](website/static/img/dashboard_page.png) | ![Inventory](website/static/img/inventory_page.png) |

| Part Details | Settings |
| :---: | :---: |
| ![Part Details](website/static/img/part_details_page.png) | ![Settings](website/static/img/settings_page.png) |

---

## Contributing

Contributions are welcome!

1. Fork the repository.
2. Create a feature branch (`git checkout -b feature/amazing-feature`).
3. Run `make generate`, `make vet`, and `make test`.
4. Commit your changes and open a Pull Request.

## License

WLEDger is released under AGPL-3.0-only — see the [LICENSE](LICENSE) file for details.
