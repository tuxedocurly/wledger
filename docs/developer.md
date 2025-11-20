---
title: WLEDger Developer Guide
layout: default
nav_order: 5
---

# Developer Guide

Welcome! This document explains the architecture, code structure, and development patterns for WLEDger.

## Core Architecture & Philosophy

This application is built with a "minimal stack" philosophy, prioritizing simplicity, robustness, and maintainability.

* **The Stack:**
    * **Go (Golang) Backend:** Chosen for performance and single-binary deployment.
    * **htmx Frontend:** Provides a modern, dynamic UI without a heavy JavaScript framework.
    * **SQLite Database:** Embedded, server-less, and zero-setup.
    * **Pico.css:** A class-less CSS framework for a clean look.

* **The Approach: Server-Side Rendered (SSR)**
    The server sends fully rendered **HTML**, not JSON. htmx intercepts clicks, makes a request, and "swaps" the HTML response.

## Code Structure (Standard Go Layout)

The project follows the Standard Go Project Layout to ensure maintainability and strict separation of concerns.

* **`cmd/server/main.go`**: The **Entrypoint**.
    * It initializes dependencies (Database, Templates, WLED Client).
    * It injects these dependencies into the `server` package.
    * It starts the HTTP server.

* **`internal/models/`**: Data Structures.
    * Contains structs like `Part`, `Bin`, `WLEDState`.
    * **Rule:** This package contains *no logic*, only definitions.

* **`internal/store/`**: The **Data Layer**.
    * This is the **only** package that imports `database/sql`.
    * It implements the database interfaces defined by the server.
    * Contains all SQL queries.

* **`internal/wled/`**: The **Hardware Layer**.
    * The HTTP client responsible for talking to WLED controllers.
    * Methods: `SendCommand`, `Ping`.

* **`internal/server/`**: The **Web & Logic Layer**.
    * **`server.go`**: Defines the `App` struct and the Interfaces (`PartStore`, `BinStore`, etc.) that the app depends on.
    * **`routes.go`**: Centralized routing logic. Defines all URL endpoints.
    * **`handlers.go`**: Handles HTTP requests. Parses forms, calls the `store`, and renders templates.
    * **`health.go`**: Background services (tickers) for health checks and cleanup jobs.

* **`ui/`**: Frontend Assets.
    * `templates/`: HTML templates (parsed by Go).
    * `static/`: CSS/JS files (served directly).

## Data Flow Example: Locating a Part

1.  **UI:** User clicks "Locate". `htmx` sends `POST /locate/part/1`.
2.  **Router:** `routes.go` directs the request to `handlers.go` -> `handleLocatePart`.
3.  **Handler:**
    * Calls `a.DashStore.GetPartLocationsForLocate(1)` (in `internal/store`).
    * The store returns a list of IPs and LEDs.
4.  **Logic:** The handler groups the LEDs by Controller IP.
5.  **Hardware:** The handler calls `a.Wled.SendCommand(...)` (in `internal/wled`).
6.  **Response:** The handler renders the `_locate-stop-button.html` template.
7.  **UI:** htmx swaps the button.

## Testing

The app is designed for testability using **Interfaces** and **Mocks**.

* **How to Run Tests:**
    ```bash
    go test -v ./...
    ```

* **Testing Philosophy:**
    * **`store_test.go`**: Integration tests. Uses a **real, in-memory SQLite DB** to verify SQL logic and constraints.
    * **`handlers_test.go`**: Unit tests. Uses **Mock Stores** to test web logic without touching a real database or network.
    * **`wled_test.go`**: Unit tests. Uses a **Fake HTTP Server** to verify JSON payloads sent to controllers.