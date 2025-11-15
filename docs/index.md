---
title: Welcome to WLEDger
layout: default
nav_order: 1
---

# Welcome to WLEDger

This site is the official user guide for the WLEDger Inventory Manager, a tool for managing hobby parts inventory with visual location tracking.

### Why Does WLEDger Exist?
As an electronics hobbyist, parts storage, management, and retrieval are a pain. I never know what parts I have, where they are, or how many of them I have on hand. I lose track of manufacturers, datasheets, and implementation guides constantly.

I built this app to solve a personal problem: managing a growing collection of electronic parts. It's difficult to remember which bin in my shop holds which component. WLEDger to the rescue! This tool links your digital inventory directly to your physical storage by using a WLED controller and RGB LEDs to illuminate the location of a given part. It's designed to be used with clear plastic bins such as [these](https://amzn.to/4nNp0bF), but works great with open shelf setup as well.

Regardless of your hobby or industry, if you have lots of parts to manage, WLEDger can help.

### Features at a Glance ###

* Per-LED Tracking: Locate parts down to the exact LED.

* Visual Stock Dashboard: See your entire inventory's status (Red/Yellow/Green) light up.

* Rich Part Details: Add images, datasheets, supplier URLs, and documents to every part.

* Fast & Minimal: Built with Go and htmx for a super-fast, lightweight experience.

* Easy to deploy on anything: Deploy the app as a docker container, or run the Go server locally. It can run on most hardware (e.g. Raspberry Pi)


**This documentation is broken into three parts:**

---

1.  **[Quick Start](./setup-guide.md)** **Start here if you are a new user.** This is a complete, end-to-end tutorial that guides you from hardware setup and WLED installation to finding your first part in WLEDger.

2.  **[Hardware Guide](./hardware-guide.md)**

3.  **[Usage Guide](./usage.md)** This is the detailed reference manual. It explains every feature on every page, including the Stock Dashboard, managing documents, adding categories, the new inspiration generator, and more.

4.  **[Developer Guide](./developer.md)** This guide is for developers. It explains the application's architecture, code structure, testing philosophy, and how to contribute.

## 🚀 Setup Process Overview

Once WLEDger is running, setting it up to work with your physical hardware is simple.

If you're new here, you'll want to reference the [full setup guide](./setup-guide.md) first.

### Prerequisites

* You have a **WLED controller** (like an ESP32 or ESP8266) set up on your network.
* You know its **IP Address**.
* You have configured your LED preferences (like segment length) in the WLED interface.
* You have the WLEDger app running.

---

### Step 1: Add Your WLED Controller

*First, we need to tell WLEDger how to contact your WLED hardware so we can control the LEDs.*

### Step 2: Add Your Bins (LEDs)

*"Bins" correspond to a single LED and storage location. This could be a plastic bin, a shelf, a drawer, or some other storage topology.*

### Step 3: Add a Part to Your Inventory

*Fill out the details for a part, such as it's name, part number, manufacturer, photo, datasheets, tags, and more!*

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