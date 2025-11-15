# WLEDger - The Inventory System That Finds Your Parts For You

A fast, lightweight, and robust inventory "ledger" for electronic parts. Powered by WLED, Go, SQLite, and htmx.


> **Note:** For the full User Guide, Build Guide, Quick-Start, and Developer Documentation, please visit the **[Official GitHub Pages Site](https://tuxedocurly.github.io/wledger/)**.

## 💡 Why I Made This

I built this app to solve a personal problem: managing a growing collection of small electronic parts. It's difficult to remember which bin or drawer in my shop holds which component.

WLEDger to the rescue! This tool links your digital inventory directly to your physical storage by using a WLED controller to light up the specific location where your part is located. It's designed to be used with clear plastic bins such as [these](https://amzn.to/47S11Cp), but works great with open shelf setup as well!

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
* **Minimal & Robust Stack:** Built with a simple, modern, and fast stack:
    * **Go (Golang)** backend for a single, fast binary.
    * **htmx** for a modern, dynamic UI *without* a heavy JavaScript framework.
    * **SQLite** for a simple, zero-setup, file-based database.
    * **Pico.css** for a clean, class-less UI.
    * **Docker** for easy, reliable deployment.

## Getting Started (How to Run)

You'll need **Go (1.25+)** and **Docker** (with Docker Compose) installed on your system.

### Option 1: Run with Docker (Recommended)

This is the simplest way to run the application in a production-like environment.

1.  **Build the image:**
    ```bash
    docker compose build
    ```
2.  **Run the container:**
    ```bash
    docker compose up -d
    ```
3.  **Access the app:**
    Open your browser to `http://localhost:3000`.

Your database and all uploaded files will be stored in persistent Docker volumes.

### Option 2: Run Locally (for Development)

1.  **Install Go dependencies:**
    ```bash
    go mod tidy
    ```
2.  **Create required directories** (the Docker container does this automatically, but you must do it locally):
    ```bash
    mkdir data
    mkdir uploads
    ```
3.  **Run the server:**
    ```bash
    go run .
    ```
4.  **Access the app:**
    Open your browser to `http://localhost:3000`.

## 📖 Documentation

* **[User Guide (`usage.md`)](https://tuxedocurly.github.io/wledger/usage)**: Explore every feature WLEDger has to offer.
* **[Build Guide (`hardware-guide.md`)](https://tuxedocurly.github.io/wledger/hardware-guide)**: A complete guide on how to build a physical WLEDger storage solution.
* **[Quick-Start Guide (`setup-guide.md`)](https://tuxedocurly.github.io/wledger/setup-guide)**: A complete guide on how to install WLED and set up WLEDger.
* **[Developer Documentation (`developer.md`)](https://tuxedocurly.github.io/wledger/developer)**: An explanation of the architecture, code structure, and how to contribute.
