<p align="center">
  <img src="docs/assets/wledger-logo.png" alt="WLEDger Logo" width="500">
</p>
<div align="center">

[![Report Bug](https://img.shields.io/badge/GitHub-Report%20Bug-black?style=for-the-badge&logo=github)](https://github.com/tuxedocurly/wledger/issues) [![Join Discord](https://img.shields.io/badge/Discord-Get%20Support-5865F2?style=for-the-badge&logo=discord&logoColor=white)](https://discord.gg/HABg37gjrd) [![Support me on Ko-Fi](https://img.shields.io/badge/Ko--Fi-Support%20Me-FF5E5B?style=for-the-badge&logo=ko-fi&logoColor=white)](https://ko-fi.com/tuxedomakes)

</div>



> **The Ultimate Inventory Management System for Makers.**
> *Organize your electronic components and find them instantly with WLED.*

Check out the official WLEDger documentation site [here](https://tuxedocurly.github.io/wledger/)
## What is WLEDger?

WLEDger (WLED + Ledger) is a modern, high-performance inventory system designed specifically for electronics hobbyists, makers, and labs. It solves the problem of "I know I have this part, but where is it?" by integrating with **WLED** controllers.

When you search for a component, WLEDger doesn't just tell you "Bin A1" — **it lights up the specific bin on your storage rack.**

<details>
<summary><strong>Why I Made WLEDger</strong></summary>
I have hundreds of electronic components in my lab. I couldn't remember what parts I had or where they were stored, leading to over-buying and under-utilizing. I wanted a system that solved these problems and got me back to doing what I love: making!
<br><br>
Existing tools were expensive, closed source or lacking in features/WLED integration. WLEDger bridges these gaps, providing a purpose-built, open source solution for the physical reality of a maker's workshop.
</details>

---

## Core Features

### Visual LED Locating System

Connect your WLED-powered LED strips or matrices to your storage.

- **Visual Locate:** Click "Locate" on a part, and its bin glows instantly.
- **Grid Painter:** Use the visual Grid Painter tool to easily map LEDs to bins using Matrix, Strip, or Compound layouts.
- **Walls / Dashboards:** Organize your containers and controllers into customizable "Walls" for a clean, organized dashboard view.
- **Stock Status Colors:** Configure different colors for "Locate", "In Stock", "Low Stock", and "Critical".

### Powerful Inventory Management

- **Fast Search:** Instant results using SQLite FTS5 (Full-Text Search).
- **Barcode Scanning:** Built-in support for scanning parts via your phone's camera.
- **Rich Data:** Store datasheets (PDFs), images, supplier links, and cost data.
- **Tagging:** Organize parts with flexible tagging.

### LLM Prompt Templates

Don't let your parts gather dust. Copy the prompt. paste it into your favorite LLM, and get inspired or informed about your inventory.

WLEDger comes with some great default prompts to get you started.

- **Project Ideas:** High quality project ideas to inpsire you to build with your *current* inventory.
- **Integration Guides:** Quick guidance on how to use your parts in a real circuit.
- **Learning Paths:** Curriculum based on the hardware you own to learn new skills.

### Enterprise-Grade Usability

- **Role-Based Access Control:** Admin, Editor, viewer, and (optional) Guest roles.
- **Audit Logging:** Track every change, stock adjustment, and deletion.
- **Responsive Design:** Designed with desktop and mobile in mind.

### Bulk Import, Backup, and Restore

Flexibility in how you manage your parts, and the data you create in WLEDger.

- **Bulk Import:** Import all your parts at once! No UI clickin' required.
- **Backup:** Easily backup all your data. Exports include all your images, docs, and part info in a human-readable format.
- **Restore:** Restore your database from a backup in a single click.

---

## The Tech Stack

WLEDger V2 has been written for performance, type safety, and extensibility.

- **Backend:** [Go](https://go.dev/) (1.25+) - Fast, compiled, and robust.
- **Database:** [SQLite](https://www.sqlite.org/) + `sqlc` + `goose` - Zero-config, reliable storage with type-safe queries and versioned migrations.
- **Frontend:** [Templ](https://templ.guide/) + [HTMX](https://htmx.org/) - Server-side rendering with SPA-like interactivity.
- **Interactivity:** [Alpine.js](https://alpinejs.dev/) - Lightweight JavaScript for UI state.
- **Styling:** [Tailwind CSS v4](https://tailwindcss.com/) + [DaisyUI v5](https://daisyui.com/).
- **Hardware:** [WLED](https://kno.wled.ge/) - The gold standard for controlling LEDs.

---

## Install WLEDger

### Option 1: Docker (Recommended)

The easiest way to run WLEDger is via Docker.

1. Create a directory for your WLEDger data: `mkdir wledger && cd wledger && mkdir data uploads logs`

2. Inside this folder, create a file named `docker-compose.yaml` with the following contents:

```yaml
services:
  wledger:
    # Format: user/repository:tag
    image: tuxedomakes/wledger:latest

    # A custom name for the container instance.
    container_name: wledger

    ports:
      # Maps ports in the format "HOST:CONTAINER"
      - "8080:8080"

    volumes:
      # Maps files in the format "HOST_PATH:CONTAINER_PATH"
      - ./data:/wledger/data
      - ./uploads:/wledger/app/uploads
      - ./logs:/wledger/app/logs

    # Restart policy
    restart: unless-stopped
```

3. Run the following command to start the container:

```BASH
docker compose up -d
```

Visit `http://localhost:8080` to see the WLEDger UI.
>Tip: Save the website as a shortcut on Android or iOS for quick access!

### Option 2: Build from Source

**Prerequisites:**

- **Go:** Version 1.25+
- **Node.js:** Version 23+ (for running `npm`)
- **Make:** A `make` compatible command line tool.

These tools are required for code generation, dependency management, and running the application.

```bash
# 1. Clone the repository
git clone https://github.com/tuxedocurly/wledger.git
cd wledger

# 2. Install dependencies
# This will install the required Go tools and npm packages.
make install_dependencies

# 3. Build the binary
make build

# 4. Run the application
./bin/wledger
```

### Development

WLEDger uses `Templ` for live reloading and `make` for task orchestration.

```bash
make dev
```

This will start:

- Go server (with Templ watcher)
- Templ + Go change watcher
- Tailwind CSS watcher

---

## UI Screenshots

| Dashboard | Inventory |
| :---: | :---: |
| ![Dashboard](docs/assets/dashboard_page.png) | ![Inventory](docs/assets/inventory_page.png) |

| Part Details | Settings |
| :---: | :---: |
| ![Part Details](docs/assets/part_details_page.png) | ![Settings](docs/assets/settings_page.png) |

---

## Contributing

Contributions are welcome!

1. Fork the repository.
2. Create a feature branch (`git checkout -b feature/amazing-feature`).
3. Commit your changes.
4. Open a Pull Request.

## License

WLEDger is released under AGPL-3.0-only - see the `LICENSE` file for details.
