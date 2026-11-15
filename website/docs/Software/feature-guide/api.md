---
title: REST API v1 & Personal Access Tokens
sidebar_position: 8
---

# REST API v1 & Personal Access Tokens

WLEDger exposes a Bearer-authenticated JSON REST API under `/api/v1/*` for scripting, CI/CD inventory syncs, label printers, and Home Assistant / workshop automation.

---

## Authentication (Personal Access Tokens)

1. Generate a scoped token in [Settings](./settings.md#personal-access-tokens-api-tokens).
2. Tokens use the `wlg_` prefix and are stored hashed (`SHA-256`) in SQLite.
3. Pass the token in the `Authorization` header:

```bash
curl -H "Authorization: Bearer wlg_your_token_here" \
  http://localhost:8080/api/v1/parts
```

### Scopes & RBAC Ceiling

| Scope | Allowed Operations |
| :--- | :--- |
| `read` | Query parts, low-stock items, containers, controllers, projects; parse barcodes; trigger LED locate / stop-locate |
| `write` | All `read` endpoints plus creating/updating/deleting parts, assigning/adjusting stock, and building projects |
| `admin` | Administrative access (bounded by the owning user's RBAC role — a `viewer` token can never escalate beyond `viewer`) |

Requests using a valid `Authorization: Bearer wlg_...` token bypass browser CSRF origin checks cleanly on `/api/v1/*`, and all mutations are attributed to the token owner in [Audit Logs](./audit-logs.md).

---

## Endpoints Reference

### Health

* `GET /api/v1/health` — Returns service health status (unauthenticated).

### Parts & Barcodes

* `GET /api/v1/parts` — Search and filter parts (`?q=`, `?tag=`, `?bin_id=`, `?limit=`, `?offset=`) (`read`)
* `POST /api/v1/parts` — Create a new part (`write`)
* `GET /api/v1/parts/{id}` — Retrieve part details (`read`)
* `PUT /api/v1/parts/{id}` — Update part metadata (`write`)
* `DELETE /api/v1/parts/{id}` — Delete a part (`write`)
* `POST /api/v1/parts/{id}/locate` — Illuminate all positive-stock WLED bins for a part (`read`)
* `GET /api/v1/low-stock` — List parts below reorder or minimum stock thresholds (`read`)
* `POST /api/v1/barcodes/parse` — Parse a DigiKey/Mouser TIA-1063, LCSC, or 1D barcode string and return matched inventory part info (`read`)

### Stock

* `POST /api/v1/stock/assign` — Assign stock to a bin or container (`write`)
* `POST /api/v1/stock/{assignmentID}/adjust` — Adjust stock quantity by ID (`write`)

### Hardware & WLED

* `GET /api/v1/containers` — List all WLED and Standalone containers (`read`)
* `GET /api/v1/controllers` — List configured WLED controllers (`read`)
* `POST /api/v1/controllers/{id}/locate` — Locate a controller or optional `bin_id` (`read`)
* `POST /api/v1/hardware/stop-locate` — Turn off locate LEDs / restore WLED state across controllers (`read`)

### Projects & BOMs

* `GET /api/v1/projects` — List projects (`read`)
* `POST /api/v1/projects/{id}/locate` — Activate Pick Mode and locate all linked BOM parts (`read`)
* `POST /api/v1/projects/{id}/build` — Atomically verify stock and deduct BOM quantities for `{"board_count": N}` (`write`)
