---
title: WLEDger Developer Guide
layout: default
nav_order: 5
---

# Developer Guide

Welcome! This document explains the architecture, code structure, and development patterns for WLEDger.

## Core Architecture & Philosophy

This application is built with a minimal stack philosophy, prioritizing simplicity, robustness, and maintainability.

* **The Stack:**
    * **Go (Golang) Backend:** Chosen for performance and single-binary deployment.
    * **htmx Frontend:** Provides a modern, dynamic UI without a heavy JavaScript framework.
    * **SQLite Database:** Embedded, server-less, and zero-setup.
    * **Pico.css:** A class-less CSS framework for clean styling out of the box.

* **The Architecture: Modular Monolith**
    * The application follows the **Standard Go Project Layout**. Instead of grouping code by technical layer (e.g. "controllers," "models"), it's grouped by **Feature Domain** (e.g. "parts," "inventory," "hardware"). This makes the codebase easier to navigate and scale.

## Code Structure

<details>
  <summary>
  <strong> Structure Visualization</strong> (Click to expand)</summary>

```
    .
    ├── cmd/
    │   └── server/
    │       └── main.go          # Application Entrypoint
    ├── data/                    # Uploads (Images, Documents)
    ├── internal/
    │   ├── background/          # Background Services
    │   │   └── service.go       # (Health, Tag cleanup)
    │   ├── core/                # Shared Application Utilities
    │   │   ├── errors_test.go   # Unit tests
    │   │   ├── errors.go        # ServerError/ClientError helpers
    │   │   └── templates.go     # Template execution interfaces
    │   ├── features/            # Web Layer (Feature Modules)
    │   │   ├── dashboard/       # Stock Dashboard logic
    │   │   ├── hardware/        # Controller management
    │   │   ├── inspiration/     # AI Prompt Generator
    │   │   ├── inventory/       # Bin & Stock management
    │   │   ├── parts/           # Part CRUD & Uploads
    │   │   ├── settings/        # Main settings page
    │   │   └── system/          # Backup/Restore logic
    │   ├── models/
    │   │   └── models.go        # Pure Data Structs
    │   ├── store/               # Data Layer (SQLite)
    │   │   ├── *_test.go        # Integration tests
    │   │   ├── store.go         # DB Init & Transactions
    │   │   └── ...              # Entity-specific queries
    │   └── wled/                # Hardware Client
    │       └── wled.go          # WLED JSON API interaction
    ├── ui/
    │   ├── static/              # CSS Assets
    │   └── templates/           # HTML Templates
    ├── docs/                    # Documentation
    ├── docker-compose.yml       # Production Runtime Config
    └── Dockerfile               # Multi-stage build definition
```
</details>

* **`cmd/server/main.go`**: The **Entrypoint**.
    * Initializes dependencies (Database, Templates, WLED Client).
    * Wires up the Feature Modules.
    * Starts the HTTP server.

* **`internal/core/`**: Shared Utilities.
    * `errors.go`: Centralized error logging and response helpers (`ServerError`, `ClientError`).
    * `templates.go`: Shared template execution logic.

* **`internal/models/`**: Data Structures.
    * Contains pure data structs like `Part`, `Bin`, `WLEDState`.
    * **Rule:** This package contains *no logic*, only definitions.

* **`internal/store/`**: The **Data Access Layer**.
    * This is the **only** package that imports `database/sql`.
    * It implements the interfaces defined by the features.
    * Files are split by entity: `parts.go`, `bins.go`, `controllers.go`.

* **`internal/wled/`**: The **Hardware Client**.
    * Responsible for sending JSON payloads to WLED controllers.

* **`internal/background/`**: Background Services.
    * Runs `time.Ticker` loops to execute health checks and cleanup jobs at regular intervals.

### Feature Modules (`internal/features/`)

This is where the application logic lives. Each folder is a self-contained module:

* **`parts/`**: Managing the Part Catalog, Images, URLs, Docs, and Categories.
* **`inventory/`**: Managing Bins and Stock levels.
* **`hardware/`**: Managing Controllers and WLED settings.
* **`dashboard/`**: The Stock Dashboard logic and "Locate" functionality.
* **`settings/`**: The composite Settings page view.
* **`system/`**: Backup, Restore, and Maintenance tasks.
* **`inspiration/`**: The LLM prompt generator.

**Anatomy of a Feature Module:**
Each feature folder contains:
1.  **`handler.go`**: Defines the HTTP handlers, routes, and the local `Store` interface it needs.
2.  **`handler_test.go`**: Contains unit tests, a local `mockStore`, and test setup helpers.

## Data Flow Example: Locating a Part

1.  **UI:** User clicks "Locate". `htmx` sends `POST /locate/part/1`.
2.  **Router:** `cmd/server/main.go` directs the request to `dashboard.Handler`.
3.  **Handler:**
    * `handleLocatePart` calls `h.store.GetPartLocationsForLocate(1)`.
4.  **Store:**
    * `internal/store/dashboard.go` runs the SQL query joining parts, bins, and controllers.
5.  **Handler:**
    * Receives the list of LEDs.
    * Groups them by Controller IP.
    * Calls `h.wled.SendCommand(...)`.
6.  **WLED Client:**
    * `internal/wled/wled.go` sends the JSON payload to the Controller.
7.  **Response:**
    * The handler renders the `_locate-stop-button.html` template partial.
    * htmx swaps the button in the browser.

## Testing

The app is designed for high testability using **Dependency Injection** and **Interface Segregation**.

> **Note:** Tests are a work in progress. If you're a testing guru and want to contribute, send a pull request :)

* **How to Run Tests:**
    ```bash
    # Run all tests (Unit + Integration)
    go test -v ./...
    ```

* **Testing Philosophy:**
    * **`internal/store/*_test.go`**: **Integration Tests.** These use a **real, in-memory SQLite database** (`:memory:`). They verify that the SQL is correct and that database constraints (Foreign Keys, Unique) work as expected.
    * **`internal/features/*/*_test.go`**: **Unit Tests.** These use **Local Mocks** defined inside the test file. They verify the HTTP logic (status codes, template rendering, error handling) without touching the database or network.