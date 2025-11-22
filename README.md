<p align="center">
  <img src="docs/assets/wledger-logo.png" alt="WLEDger Logo" width="500">
</p>

<p align="center">
  <a href="https://github.com/tuxedocurly/wledger/issues"><img src="https://img.shields.io/badge/GitHub-Report%20Bug-black?style=for-the-badge&logo=github" alt="Report Bug" /></a>&nbsp;&nbsp;&nbsp;
  <a href="https://discord.gg/HABg37gjrd"><img src="https://img.shields.io/badge/Discord-Get%20Support-5865F2?style=for-the-badge&logo=discord&logoColor=white" alt="Join Discord" /></a>&nbsp;&nbsp;&nbsp;
  <a href="https://ko-fi.com/tuxedomakes"><img src="https://img.shields.io/badge/Ko--Fi-Support%20Me-FF5E5B?style=for-the-badge&logo=ko-fi&logoColor=white" alt="Support me on Ko-Fi" /></a>
</p>

# The Inventory System That Finds Your Parts For You

A fast, lightweight, and robust inventory "ledger" for managing inventory and physically locating parts. Powered by WLED, Go, SQLite, Pico CSS, and HTMX.


> **Note:** For the full User Guide, Build Guide, Quick-Start, and Developer Documentation, please visit the **[Official GitHub Pages Site](https://tuxedocurly.github.io/wledger/)**.

## 💡 Why I Made This

I built this app to solve a personal problem: managing a growing collection of small electronic parts. It's difficult to remember which bin or drawer in my shop holds which component.

WLEDger to the rescue! This tool links your digital inventory directly to your physical storage by using a WLED controller to light up the specific location where your part is located. It's designed to be used with clear plastic bins such as [these](https://amzn.to/47S11Cp), but works great with open shelf setup as well!

<div align="center">
    <h2>Pages UI</h2>
    <img src="docs/assets/wledger-pages-demo.gif" alt="WLEDger Pages Demo GIF" width="700" align:>
    <h2>Part Manager UI</h2>
    <img src="docs/assets/wledger-part-demo.gif" alt="WLEDger Part Manager Demo GIF" width="692">
</div>


## Core Features

* **Per-LED Bin Location:** Maps parts to individual LEDs on a WLED segment for precision locating.
* **Visual Stock Dashboard:** A top-level dashboard that lights up all bins to show stock levels at a glance (Green for "OK," Yellow for "Low," Red for "Critical").
* **Get Inspired With LLM Prompt Generation:** Creates custom prompts for LLMs (like ChatGPT/Gemini) to generate project ideas based on your in-stock parts. Copy the generated prompt into your favorite LLM to get project recommendations.
* **Search Bar:** Easily search your parts using their name, description, part number, manufacturer, category/tag, and more.
* **Rich Part Management:** Store detailed information for each part, including:
    * Images (File Uploads)
    * External URLs (Datasheets, supplier links, YouTube videos, etc.)
    * Local Documents (PDFs, schematics)
    * Categories & Tags
    * Stock Tracking (Min/Reorder levels)
    * Supplier & Manufacturer Info
* **Hardware Health Checks:** The app proactively pings your WLED controllers to show their "Online" or "Offline" status in the UI. Have unused or accidentally misspelled tags? A tag cleanup job removes any unused tags automatically.
* **Backup & Restore:** Your data is *your* data. Export your entire database and image library to a standard ZIP file for safekeeping or migration.
* **Minimal & Robust Stack:** Built with a simple, modern, and fast stack:
    * **Go (Golang)** backend for a single, fast binary.
    * **htmx** for a modern, dynamic UI *without* a heavy JavaScript framework.
    * **SQLite** for a simple, zero-setup, file-based database.
    * **Pico.css** for a clean, class-less UI.
    * **Docker** for easy, reliable deployment.

## Getting Started (How to Run)

### Option 1: Run with Docker (Recommended)

> You'll need **Docker** (with Docker Compose) installed on your system.

### Docker Hub

This is the easiest option.

1.  **Create docker-compose.yml file:**
    ```yaml
    services:
        wledger:
            image: tuxedomakes/wledger:latest
            container_name: wledger
            restart: always
            ports:
                - "7483:3000"
        volumes:
        # IMPORTANT:
        # Change ./wledger_data to the directory where
        # you want to store your DB, img, and upload data
            - ./wledger_data:/app/data 

    volumes:
        wledger_data:

    ```
2.  **Run the container:**
    ```bash
    docker compose up -d
    ```
3.  **Access the app:**
    Open your browser to `http://localhost:3000`.

### (Optional) Build the Docker Image Yourself

If you don't want to use the docker hub image above, you can build the docker image yourself.

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/tuxedocurly/wledger.git
    ```
2.  **Build the image:**
    ```bash
    docker compose build
    ```
3.  **Run the container:**
    ```bash
    docker compose up -d
    ```
4.  **Access the app:**
    Open your browser to `http://localhost:3000`.

### Option 2: Run Locally (for Development)

> You'll need **Go (1.25+)** installed on your system.

1.  **Install Go dependencies:**
    ```bash
    go mod tidy
    ```
2.  **Run the server:**
    ```bash
    go run ./cmd/server
    ```
3.  **Access the app:**
    Open your browser to `http://localhost:3000`.

## 📖 Documentation

* **[User Guide (`usage.md`)](https://tuxedocurly.github.io/wledger/usage)**: Explore every feature WLEDger has to offer.
* **[Build Guide (`hardware-guide.md`)](https://tuxedocurly.github.io/wledger/hardware-guide)**: A complete guide on how to build a physical WLEDger storage solution.
* **[Quick-Start Guide (`setup-guide.md`)](https://tuxedocurly.github.io/wledger/setup-guide)**: A complete guide on how to install WLED and set up WLEDger.
* **[Developer Documentation (`developer.md`)](https://tuxedocurly.github.io/wledger/developer)**: An explanation of the architecture, code structure, and how to contribute.
