---
title: Developer Guide
sidebar_position: 2
---

# Developer Guide

Welcome to the WLEDger Developer Guide. This document details the Pure-Go architecture, project structure, and development workflow.

## Tech Stack

* **Backend:** [Go 1.25+](https://go.dev/) — Static Pure-Go builds (`CGO_ENABLED=0`) with embedded assets (`embed.FS`), locales, and SQL migrations.
* **Web Framework:** [Chi v5](https://github.com/go-chi/chi) (Router) + Go 1.25 `http.NewCrossOriginProtection` CSRF middleware.
* **Database:** Pure-Go [SQLite](https://www.sqlite.org/) via [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) with **FTS5** (Full-Text Search), WAL mode, and serialized write transactions.
* **Data Access:** [SQLC](https://sqlc.dev/) (Type-safe SQL code generation) + [Goose](https://github.com/pressly/goose) (embedded migrations).
* **Templating:** [Templ](https://templ.guide/) (`v0.3.977`).
* **Frontend Interactivity:** [HTMX 2.0](https://htmx.org/) + [Alpine.js 3](https://alpinejs.dev/) + [SortableJS](https://sortablejs.github.io/Sortable/) + [html5-qrcode](https://github.com/mebjas/html5-qrcode) (all vendored locally under `web/static/vendor/`).
* **Styling:** [Tailwind CSS v4](https://tailwindcss.com/) + [DaisyUI v5](https://daisyui.com/).

## Project Structure

```text
wledger/
├── cmd/
│   └── server/         # Main entry point (main.go), TLS setup, graceful SIGTERM shutdown
├── internal/           # Private application packages
│   ├── api/            # Bearer-authenticated JSON REST API v1 (/api/v1/*)
│   ├── apitoken/       # Scoped Personal Access Tokens (wlg_..., SHA-256 at rest)
│   ├── audit/          # Structured audit log recording
│   ├── auth/           # RBAC, bcrypt password policy, SCS session store
│   ├── backup/         # ZIP backup export & atomic restore with ZipSlip/bomb guards
│   ├── barcodes/       # 2D supplier barcode parser (TIA-1063 DigiKey/Mouser, LCSC, 1D)
│   ├── config/         # WLEDGER_* environment-driven runtime config
│   ├── dashboard/      # Dashboard stats, container grids, and walls
│   ├── db/             # Pure-Go SQLite driver, concurrency lock, sqlc queries & goose runner
│   ├── documents/      # Extension- and MIME-validated document uploads
│   ├── handler/        # HTTP & HTMX request handlers
│   ├── hardware/       # WLED controller & standalone container hierarchy + Grid Painter
│   ├── i18n/           # Embedded localization bundle + parity test suite
│   ├── images/         # Image resizing with DecodeConfig bomb protection
│   ├── importer/       # CSV bulk importer (supports standalone containers)
│   ├── inspiration/    # Offline LLM prompt templates
│   ├── logger/         # Dynamic-level JSON slog logger
│   ├── middleware/     # Single-lookup auth, setup cache, rate limiter, upload limits
│   ├── parts/          # Parts CRUD & sanitized FTS5 search
│   ├── projects/       # Projects, KiCad/CSV BOM import, Pick Mode & atomic builds
│   ├── router/         # Chi router, CSRF, hardened /uploads/*, /api/v1/* mount
│   ├── settings/       # System settings & user management
│   ├── stock/          # Multi-location & orphaned stock adjustments
│   ├── tags/           # Tag CRUD & search syncing
│   ├── tlsutil/        # Explicit TLS & auto-generated ECDSA P-256 LAN certificates
│   ├── uierror/        # HTMX 2.0 OOB error toast responder
│   └── wled/           # Stateful WLED client, mDNS discovery & UDP Realtime DNRGB
├── locales/            # Embedded active.*.json translation files (8 languages)
├── sql/
│   ├── schema/         # Embedded goose migrations
│   └── queries/        # sqlc SQL definitions
└── web/                # Embedded static assets, vendor JS, Templ layouts/pages/components
```

## Development Workflow

### Prerequisites

* Go 1.25+
* Node.js 22+ (for Tailwind CSS CLI compilation)
* Make

### Running Locally

```bash
make install_dependencies  # Installs pinned templ, sqlc, and npm packages
make generate              # Runs templ generate, sqlc generate, and Tailwind CSS build
make dev                   # Starts Templ proxy + live Tailwind watcher
```

The dev server starts at `http://localhost:8080` (Templ live-reload proxy at `http://localhost:7331`).

### Database Changes

1. **Schema Migrations:** Add a new SQL migration in `sql/schema/` (`00X_description.sql`). Note: SQLite ignores `PRAGMA foreign_keys = OFF` inside transactions — use `-- +goose NO TRANSACTION` if recreating foreign-key-referenced tables.
2. **SQL Queries:** Add or update queries in `sql/queries/`.
3. **Generate Code:** Run `make generate` (or `sqlc generate`).

### Localization & Parity Tests

All 8 translation files (`locales/active.*.json`) are embedded via `locales/embed.go`. `internal/i18n/new_languages_test.go` automatically verifies:

* All 8 locale JSON files parse cleanly and share the exact same key set with non-empty `other` strings and matching template placeholders.
* Every literal `i18n.T(ctx, "...")` and `i18n.TD(ctx, "...")` call across `web/` and `internal/` exists in `locales/active.en.json`.

### Verification & Tests

```bash
make vet     # Runs go vet ./...
make test    # Runs go test -race -count=1 ./...
```

### Building for Production

WLEDger uses `modernc.org/sqlite`, so production builds require **no CGO and no C compiler**:

```bash
make build
```

This runs `templ generate`, minifies Tailwind CSS, and compiles a static pure-Go binary at `bin/wledger` (`CGO_ENABLED=0`). You can also run `./scripts/verify_build.sh` to validate `go vet`, `CGO_ENABLED=0` static compilation, and pure-Go tests end-to-end.

## Contributing

1. Fork the repository.
2. Create a feature branch (`git checkout -b feature/amazing-feature`).
3. Run `make generate`, `make vet`, and `make test`.
4. Commit your changes and open a Pull Request.
