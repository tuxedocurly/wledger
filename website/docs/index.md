---
title: Welcome to WLEDger
slug: /
sidebar_position: 1
---

# Welcome to WLEDger

WLEDger is a high-performance inventory management system for electronics hobbyists, makers, and workshops. It bridges the gap between your digital inventory and physical storage by integrating with [WLED](https://kno.wled.ge/) controllers to physically light up the exact location of your parts — while also supporting controller-less standalone storage containers.

Built as a single static **Pure-Go** binary (`CGO_ENABLED=0`) powered by **Templ**, **HTMX**, and pure-Go **SQLite**, WLEDger runs anywhere from a Raspberry Pi to Docker with zero external runtime dependencies.

## Key Features

* **Visual WLED Location Tracking:** Light up individual bins or multi-LED wide bins via HTTP JSON API or high-speed **UDP Realtime (DNRGB)**, with **mDNS auto-discovery**, **pre-locate state restoration**, and optional stock-health locate colors.
* **Standalone Containers:** Organize drawers, travel cases, and shelves without needing a WLED controller, and bind or detach controllers at any time.
* **2D Supplier Barcode Scanning:** Scan or paste **DigiKey**, **Mouser** (TIA-1063 DataMatrix/PDF417), **LCSC** (QR/DataMatrix), and 1D barcodes to auto-fill part specs and match existing inventory.
* **Projects & BOM Pick Mode:** Import KiCad/CSV BOMs, track multi-board shortages, illuminate every required bin simultaneously in **Pick Mode**, and execute atomic project builds.
* **Built-In Optional HTTPS:** Enable `WLEDGER_TLS_SELF_SIGNED=true` for an auto-generated ECDSA P-256 LAN certificate so mobile camera scanning and clipboard copying work smoothly over HTTPS.
* **REST API v1 & Personal Access Tokens:** Automate workflows with Bearer-authenticated `/api/v1/*` endpoints and scoped `wlg_...` tokens.
* **Instant Search:** Powered by pure-Go SQLite **FTS5**, search your entire inventory, tags, MPNs, and descriptions in milliseconds.
* **Custom Walls / Dashboards:** Organize your storage containers into logical "Walls" for a clean overview of your workshop.
* **LLM Prompt Integration & Backups:** Generate structured inventory prompts for LLMs (Gemini, ChatGPT, Claude, or local models) and export/restore portable ZIP backups.

### Documentation

1. **[Quick Start](./Software/quickstart-guide.md)**
    * Install WLEDger via Docker or single-binary release, optionally enable LAN HTTPS, and configure your first WLED controller or standalone container.

2. **[Build Guide](./Hardware/build-guide.md)**
    * Learn about the hardware requirements: microcontrollers, LED strips, power supplies, custom PCBs, and storage bin recommendations.

3. **[Feature Guide](./Software/feature-guide/dashboard.md)**
    * Deep dive into the Dashboard, Inventory & 2D Barcode Scanning, Projects & BOM Pick Mode, Hardware & WLED v2, REST API v1, Inspiration, Audit Logs, and Settings.

4. **[Developer Guide](./Software/developer-guide.md)**
    * Explore the Pure-Go architecture, `sqlc`/`templ`/`tailwindcss` workflow, embedded assets, and testing.

---

### How It Works

1. **Store It:** Add parts manually, scan 2D DigiKey/Mouser/LCSC bag labels, or bulk import via CSV.
2. **Map It:** Use **Hardware** to auto-discover WLED controllers (or create Standalone Containers) and map LEDs with the Grid Painter.
3. **Find & Build:** Click **Locate** on any part — or open a **Project** in **Pick Mode** — and watch the right bins light up in your workshop.

### Community & Support

* **GitHub:** [tuxedocurly/wledger](https://github.com/tuxedocurly/wledger)
* **Discord:** [Join the WLEDger Discord](https://discord.gg/wmDdcar7Kq)
* **Donate:** Enjoying WLEDger? Consider [buying me a coffee](https://ko-fi.com/tuxedomakes)
