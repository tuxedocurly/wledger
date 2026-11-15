---
title: Hardware (WLED & Containers)
sidebar_position: 4
---

# Hardware & Storage Integration (WLED v2)

WLEDger's defining feature is bridging digital inventory and physical storage. Connect WLED controllers to illuminate exact bins on demand — or use controller-less **Standalone Containers** when organizing bins without LEDs.

---

## Standalone Containers (No WLED Required)

You do not need a WLED controller (or any dummy `0.0.0.0` IP workaround) to organize drawers, travel cases, or shelves in WLEDger:

1. On **Hardware**, click **Add Standalone Container**.
2. Enter a container **Name** and grid dimensions (**Rows** × **Cols**).
3. Click **Configure** to customize the grid, label bins, or set optional future LED indices.
4. Mount Standalone Containers onto Dashboard **Walls** alongside WLED-backed containers, and assign parts to them directly from the Inventory bin picker or CSV bulk import.
5. Anytime you add a WLED strip later, use the container's **Bind** selector on **Hardware** to attach it to a controller (or detach it back to Standalone).

---

## WLED Controllers

WLEDger communicates with ESP32/ESP8266 controllers running [WLED](https://kno.wled.ge/) via the HTTP JSON API and optional **UDP Realtime (DNRGB)** pixel streaming.

### mDNS Auto-Discovery & DHCP Self-Healing

1. Click **Discover WLED** on **Hardware** to broadcast an mDNS `_wled._tcp.local.` query (`224.0.0.251:5353`) on your LAN and verify `/json/info`.
2. Click **Add** on any newly discovered node.
3. If an existing controller's IP changed after a router reboot, mDNS discovery matches its **MAC address** and automatically updates the stored IP address.

### Controller Options (Add & Edit)

Click **Edit** on any controller card to configure WLED v2 options:

| Field | Purpose | Usage Tips |
| :--- | :--- | :--- |
| **Name** | A friendly name (e.g., `Main Workbench`). | Helps identify which physical rack or bench is controlled. |
| **IP Address** | The LAN IPv4/IPv6 address or hostname of the WLED device. | Validated strictly (`0.0.0.0`, schemes, and invalid hosts are rejected). |
| **Port** | HTTP API port (default `80`). | All WLED HTTP requests honor this custom port. |
| **Use UDP Realtime (DNRGB)** | Stream bin LED colors via WLED UDP Realtime (`DNRGB`). | Ideal for fast updates and large multi-LED setups. Automatically chunks payloads above 489 LEDs. |
| **UDP Port** | UDP Realtime listen port (default `21324`). | Match WLED's **Sync Interfaces -> UDP Port** setting. |
| **Restore Previous State** | Snapshot `/json` state before Locate and restore it afterward. | When enabled, ambient lights or effects resume seamlessly once Locate times out or **Turn Off All LEDs** is clicked. |
| **Idle Preset ID** | Optional WLED Preset ID (`0 = Off`) to activate after Locate finishes. | Overrides state restoration when `> 0`; if `0` and restoration is disabled, all segments clear and power off (`"on": false`). |

---

## Configure the Grid Painter

### Layout Types

| Type | Best For | Description |
| :--- | :--- | :--- |
| **Linear Strip** | Shelf edges, single-row drawers. | A sequential run of LEDs. Indices increment by 1. |
| **Matrix** | 2D panels or drawer grids. | Defines a 2D grid (e.g., 8×8). |
| **Compound** | Irregular storage racks. | Groups multiple linear or matrix sections into one logical container. |

### Mapping Bins & Multi-LED Width

1. Click **Configure** on a controller (or on a Standalone Container).
2. Set the container name, **WLED Segment ID** (default `0`), layout type, data start position, and dimensions.
3. Use **Auto Map (Linear)** or **Auto Map (Snake)**, or select **Paint** / **Erase** in the visual grid to map individual LED indices.
4. Use the **Bin Inspector** to set **LED Width** (`Width >= 1`) when a physical drawer spans multiple adjacent LEDs — locating a part illuminates `[led_index, led_index + width - 1]` simultaneously.
5. Click **Save Configuration**.

---

## Locating Parts & Stateful Behavior

* **Resettable Timers:** Re-clicking **Locate** on the same controller cancels and resets its auto-off timer cleanly without cutting off newer locates early.
* **Batched Multi-Bin Locate:** Locating a part (or using **Pick Mode** in [Projects](./projects.md)) batches all positive-stock bins per controller and segment into one clean update.
* **Stock-Health Colors:** In [Settings](./settings.md), you can configure Locate to color each bin by stock health (`OK`, `Low`, `Critical`) instead of a single Locate color.
* **Error Feedback:** Unreachable controllers or WLED network errors surface an immediate error toast in the UI.
