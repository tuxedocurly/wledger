---
title: Projects & BOM Pick Mode
sidebar_position: 7
---

# Projects & BOM Kitting ("Pick Mode")

WLEDger V2 brings **Projects** and **BOM Kitting** directly into your workshop workflow — letting you import KiCad or CSV Bills of Materials, check multi-board stock shortages, illuminate every needed bin at once in **Pick Mode**, and deduct inventory atomically when you build a board.

---

## Creating a Project & Importing a BOM

1. Navigate to **Projects** (`/projects`) and click **Create Project** with a name, description, and status (`draft`, `active`, etc.).
2. Open the project detail page (`/projects/{id}`).
3. Add BOM lines manually **or** click **Import BOM** to upload or paste a KiCad / CSV BOM.

### Supported BOM CSV Columns

WLEDger automatically detects common KiCad and EDA BOM column headers:

| Field | Accepted CSV Headers |
| :--- | :--- |
| **Designator** | `Reference`, `Designator` |
| **Part Name / Value** | `Value`, `Name` |
| **MPN** | `MPN`, `Manufacturer Part Number`, `Part Number` |
| **Quantity** | `Qty`, `Quantity` |
| **Description** | `Description`, `Comment` |
| **Notes** | `Notes` |

During import, WLEDger automatically attempts to link each BOM line to an existing inventory part in priority order:

1. Case-insensitive **MPN (`part_number`)** match
2. **Barcode** match
3. Exact **Part Name** match

You can also link or re-link any BOM item manually to an inventory part at any time.

---

## Multi-Board Shortage Calculation

Use the board multiplier (`?boards=N`) on the Project Detail page to plan batch builds (for example, assembling `5` PCBs):

* **Required Qty:** `quantity_per_board × boards`
* **Shortage Indicator:** Highlights any unlinked lines or lines where available stock is below required quantity, alongside an overall **Buildable** status for the requested board count.

---

## Pick Mode (`Locate All`)

Instead of hunting through bins one part at a time while stuffing a PCB:

* **Pick Mode (`Locate All`):** Click **Locate All** (`POST /projects/{id}/locate`) to simultaneously illuminate **every bin** containing positive stock for the project's linked BOM items across all WLED controllers.
* **Single-Item Locate:** Click **Locate** on an individual BOM row (`POST /projects/{id}/bom/{itemID}/locate`) to highlight just that component's bins.

---

## Atomic Build Project

When you finish kitting or assembling `N` boards, click **Build Project** (`POST /projects/{id}/build`):

1. Inside a single SQLite transaction, WLEDger verifies that **every** required BOM item is linked and has sufficient stock for the requested board count.
2. If any component is short or unlinked, the transaction rolls back cleanly with a descriptive shortage error — no partial deductions ever occur.
3. If all parts are available, WLEDger deducts the exact required quantities deterministically across part assignments and writes `STOCK_DEC` and `PROJECT` entries to [Audit Logs](./audit-logs.md).
