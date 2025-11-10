---
title: Welcome to WLEDger
layout: default
nav_order: 1
---

# Welcome to WLEDger

This site is the official user guide for the WLEDger Inventory Manager, a tool for managing hobby parts inventory with visual location tracking.

This documentation is broken into three parts:

---

1.  **[Full Setup & Quick Start](./setup-guide.md)** **Start here if you are a new user.** This is a complete, end-to-end tutorial that guides you from hardware setup and WLED installation to finding your first part in WLEDger.

2.  **[Full User Guide](./usage.md)** This is the detailed reference manual. It explains every feature on every page, including the Stock Dashboard, managing documents, adding categories, and more.

3.  **[Developer Guide](./developer.md)** This guide is for developers. It explains the application's architecture, code structure, testing philosophy, and how to contribute.

## 🚀 Quick Start Guide

This guide will walk you through the "Aha!" moment of the app: adding your hardware, adding a part, and lighting up the LED to find it.

### Prerequisites

* You have a **WLED controller** (like an ESP32 or ESP8266) set up on your network.
* You know its **IP Address**.
* You have configured your LED preferences (like segment length) in the WLED interface.
* You have the WLED Inventory Manager app running.

---

### Step 1: Add Your WLED Controller

First, we need to tell the app how to contact your hardware.

1.  Navigate to the **Settings** page.
2.  Under the "Manage WLED Controllers" section, enter a **Name** (e.g., "Main Shelf") and the **IP Address** of your controller.
3.  Click **"Add Controller"**.
4.  Your controller will appear in the "Existing Controllers" list. Click the `🔄` refresh button to confirm its status is "● Online".

### Step 2: Add Your Bins (LEDs)

Next, we'll create the individual "bins" that correspond to each LED on your strip. We'll use the bulk-add tool.

1.  On the **Settings** page, find the "Manage Bins" section.
2.  Fill out the **"Bulk Add Segment Bins"** form:
    * **WLED Controller:** Select the "Main Shelf" you just added.
    * **Segment ID:** Enter the WLED segment you're using (usually `0`).
    * **Number of LEDs:** Enter the total number of LEDs on that segment (e.g., `64`).
    * **Bin Name Prefix:** Enter a name (e.g., `A1-`). This is crucial for identification.
3.  Click **"Add Segment Bins"**.
4.  The page will reload, and the "Existing Bins" table will now be populated with `A1-0`, `A1-1`, `A1-2`, ..., `A1-63`.

### Step 3: Add a Part to Your Catalog

Now, let's add a part to our inventory.

1.  Navigate to the **Inventory** page.
2.  Expand the **"Add New Part Type"** form.
3.  Fill in the details for your part (e.g., Name: `220 Ohm Resistor`, Manufacturer: `Yageo`).
4.  Click **"Add Part Type"**. Your new part will appear in the "My Parts Catalog" list.

### Step 4: Add Stock to a Bin

Now, let's digitally "place" that part into a physical bin.

1.  On the **Inventory** page, click the new **"220 Ohm Resistor"** link. This takes you to the Part Details page.
2.  Find the **"Add to New Bin"** section.
3.  In the "Bin" dropdown, select the bin where you are storing the part (e.g., **`A1-0 (Seg: 0, LED: 0)`**).
4.  Enter the **Quantity** (e.g., `100`).
5.  Click **"Add Stock"**.
6.  The page will reload, and your part will now appear in the "Inventory Locations" table.

### Step 5: Locate Your Part!

This is the final step.

1.  Navigate back to the main **Inventory** page.
2.  Find your "220 Ohm Resistor" in the list.
3.  Click its **"Locate"** button.

The first LED on your strip (`A1-0`) should instantly light up bright red, showing you exactly where your part is. Click **"Stop"** to turn it off.

---

## Next Steps

You've completed the basic workflow! To learn about all the other features, check out the detailed guides:

* **[Full User Guide](./usage.md)**: Learn how to manage stock, use the dashboard, upload images, add documents, and more.
* **[Developer Guide](./developer.md)**: Learn about the app's architecture and how to contribute.