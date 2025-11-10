---
title: WLEDger Developer Guide
layout: default
nav_order: 4
---

# Developer Guide

Welcome! This document explains the architecture, code structure, and development patterns for the WLEDger Inventory Manager.

Contributions, bug reports, and feature requests are welcome! To contribute, submit a pull request. To file a bug report or a feature request, submit a new Issue.

##  Core Architecture & Philosophy

This application is built with a "minimal stack" philosophy, prioritizing simplicity, robustness, and maintainability.

* **The Stack:**
    * **Go (Golang) Backend:** Chosen for its performance and ability to compile into a single, dependency-free binary.
    * **htmx Frontend:** Chosen to provide a modern, dynamic UI *without* a complex JavaScript framework. It allows us to write all our logic in Go and simply swap HTML fragments.
    * **SQLite Database:** Chosen for its simplicity. It's an embedded, server-less, file-based database, which is perfect for this type of self-hosted application.
    * **Pico.css:** A class-less CSS framework that provides a clean, modern look with zero setup.

* **The Approach: Server-Side Rendered (SSR)**
    The server sends fully rendered **HTML**, not JSON. htmx intercepts clicks, makes a request, and then "swaps" a piece of the current page with the HTML response from the server. This keeps all application logic, state, and templating in one place (the Go backend).

## Code Structure (Separation of Concerns)

All Go code lives in `package main`. The project is organized into files based on their "concern" or responsibility:

* **`main.go`**: The entrypoint. This file is responsible for:
    1.  Defining the core `App` struct.
    2.  Defining the data `interfaces` (e.g., `PartStore`, `BinStore`).
    3.  Initializing dependencies (Store, WLED Client, Templates).
    4.  Registering all HTTP routes.
    5.  Starting the background services and the web server.

* **`models.go`**: Data structures only. This file defines *what* our data looks like (e.g., the `Part`, `Bin`, `WLEDState` structs). It contains no logic.

* **`store.go`**: The "Data Layer." This is the **only** file that talks to the database.
    * It defines the `Store` struct, which implements all our data interfaces.
    * It contains all SQL queries and database logic.
    * Handlers *never* write SQL; they call methods on the store (e.g., `a.partStore.GetPartByID(1)`).

* **`handlers.go`**: The "Web Layer." This file is responsible for handling HTTP requests and responses.
    * It contains all the `(a *App) handle...` functions.
    * It parses forms, calls the `store` for data, and executes the HTML templates.

* **`wled.go`**: The "Hardware Client." This is the **only** file that knows how to communicate with WLED.
    * It defines the `WLEDClient` and `WLEDClientInterface`.
    * It contains the low-level `SendCommand` and `Ping` methods that send HTTP requests to the WLED JSON API.

* **`health.go`**: Background Services.
    * Contains the `startBackgroundServices` function.
    * Runs all `time.Ticker` logic for periodic jobs (like WLED health checks and tag cleanup).

* **`*_test.go`**: All test files. They live next to the code they are testing.

## Data Flow Example: Locating a Part

Understanding this flow is key to understanding the app.

1.  **UI:** A user clicks the "Locate" button on the main page.
2.  **htmx:** The button's `hx-post="/locate/part/1"` attribute sends a `POST` request to the server.
3.  **`main.go`:** The Chi router matches the route and calls `app.handleLocatePart`.
4.  **`handlers.go`:**
    * `handleLocatePart` is executed.
    * It calls `a.dashStore.GetPartLocationsForLocate(1)` to get the data.
5.  **`store.go`:**
    * `GetPartLocationsForLocate` runs its SQL query, joining `parts`, `bins`, and `controllers` to find all LEDs for Part 1.
    * It returns a list of IPs, Segment IDs, and LED Indices.
6.  **`handlers.go`:**
    * The handler receives this list.
    * It groups the LEDs by IP and Segment ID.
    * It calls `a.wled.SendCommand(...)` for each controller.
7.  **`wled.go`:**
    * `SendCommand` builds the WLED JSON payload (e.g., `{"seg":[{"id":0, "i":[5, "FF0000"]}]}`).
    * It sends the `POST` request to the WLED controller.
8.  **`handlers.go`:**
    * After the command succeeds, the handler renders the `_locate-stop-button.html` template.
9.  **htmx:** The client receives the "Stop" button HTML and swaps it into the `div` on the page.

## Testing

The app is built to be testable by separating its concerns.

* **How to Run Tests:**
    ```bash
    # Run all tests
    go test ./...
    
    # Run all tests with verbose output
    go test -v ./...
    ```

* **Testing Philosophy:**
    * **`store_test.go`**: Tests the Data Layer. It creates a **real, in-memory** SQLite database (`:memory:`) and runs *actual* SQL queries against it to verify `CREATE`, `GET`, `UPDATE`, `DELETE`, and constraint logic.
    * **`handlers_test.go`**: Tests the Web Layer. It uses **mocks** (`mockPartStore`, `mockWLEDClient`). We don't use a real database. We test that the handler:
        1.  Calls the correct store methods.
        2.  Behaves correctly (e.g., returns `409 Conflict` when the mock store returns `ErrForeignKeyConstraint`).
        3.  Returns the correct HTTP status code and HTML.
    * **`wled_test.go`**: Tests the Hardware Client. It creates a **fake WLED server** (`httptest.NewServer`) and confirms that our `WLEDClient` sends the correct JSON payload.