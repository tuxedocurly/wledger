---
title: WLEDger Quick Start
layout: default
nav_order: 2
---

# Full Setup & Quick Start Guide

Welcome to WLEDger! This guide will walk you through the entire process, from setting up your hardware to finding your first part with an LED.

## Section 1: Hardware Requirements

Before you begin, you'll need a few pieces of hardware.

### Controllers
WLEDger works with any microcontroller compatible with WLED. Here are some popular, easy-to-use options:

* **Adafruit Sparkle Motion Mini:** An all-in-one board with a level shifter, terminal block, and USB-C.
* **WEMOS D1 Mini (ESP8266):** A very common and inexpensive board.
* **ESP32 Dev Kit:** A powerful and popular board with plenty of processing power.

### LED Strips
You'll need a compatible addressable LED strip, such as:
* WS2812B (also known as NeoPixel)
* SK6812

### 🚨 Power & Safety (Important!)

This is the most critical part of your hardware setup.

* **DO NOT** power a long LED strip (more than 10-15 LEDs) directly from your computer's USB port or the microcontroller's 5V pin. This could damage your computer or your board.
* **ALWAYS** use a separate, external 5V power supply.
* **ALWAYS use a fuse** of the appropriate size between your power supply and your LEDs to protect from shorts and excess current draw
* **Rule of Thumb:** A common rule is `60mA` (0.06A) per LED at full white brightness (peak power draw scenario). For 64 LEDs, you would need `64 * 0.06A = 3.84A`. A **5V 4A** power supply would be a good choice.
    * **Note:** This is probably a vast overbudgeting of the power requirement. The real world current draw for the "rainbow" effect on 128 LEDs in my testing setup is 1.8A. Peak current draw while using WLEDger was .5A with an idle draw of .2A.
* **Wiring:** Connect your external 5V power supply to the LED strip's `+5V` and `GND` inputs directly. Then, connect the strip's `GND` pin to your microcontroller's `GND` pin. Finally, connect the strip's `Data` pin to your microcontroller's data pin.

If LED power management isn't something you feel comfortable managing yourself, I highly recommend getting a microcontroller that is build with LED control in mind, such as the [Adafruit Sparkle Motion Mini](https://www.adafruit.com/product/6160). This board comes with an ESP32 for running WLED, and is capable of delivering 5v 4A to your project. It's an awesome board.

---

## Section 2: WLED Installation & Setup

Now, let's flash the WLED software onto your controller.

### 1. Flashing WLED
The easiest method is using the web flasher.
1.  Plug your controller into your computer via USB.
2.  Go to the official **[WLED Web Flasher](https://install.wled.me/)**.
3.  Click "Connect" and select the serial port for your device.
4.  Choose the latest stable version of WLED and click "Install."

### 2. Configuring WLED
1.  Once flashing is complete, your controller will create a Wi-Fi access point named **`WLED-AP`**.
2.  Connect to this Wi-Fi network with your phone or computer. The password is `wled1234`.
3.  Your browser should open to the WLED interface. Go to **Config > Wi-Fi Setup**.
4.  Enter your home Wi-Fi credentials and click "Save."
5.  Your controller will restart and connect to your network. Find its new IP address from your router's device list.

### 3. Testing Your Strip
1.  Using any device on your network, go to the new **IP Address** of your controller (e.g., `http://192.168.1.50`).
2.  Go to **Config > LED Preferences**.
3.  Set the **"Length"** to the total number of LEDs on your strip (e.g., `64`).
4.  Click "Save."
5.  Go back to the main WLED page. Use the color palette to turn on your lights. **Confirm they work in WLED before proceeding.**

---

## Section 3: WLEDger Installation

Now that your hardware is working, let's run the WLEDger software.

### 1. Install Dependencies
You only need one piece of software:
* **[Docker Desktop](https://www.docker.com/get-started/)**

### 2. Run WLEDger
1.  Download the WLEDger project files (e.g., as a ZIP from GitHub or using `git clone`).
2.  Open a terminal in the project's root folder (where `docker-compose.yml` is).
3.  Build the app (you only need to do this once):
    ```bash
    docker compose build
    ```
4.  Run the app:
    ```bash
    docker compose up
    ```
5.  Access the WLEDger UI by opening your browser to **`http://localhost:3000`**.

---

## Section 4: WLEDger Quick Start

Let's link your software inventory to your new hardware.

### Step 1: Add Your Controller
1.  In the WLEDger UI, navigate to the **Settings** page.
2.  Under "Manage WLED Controllers," enter a **Name** (e.g., "Main Shelf") and the **IP Address** of your controller (from Section 2).
3.  Click **"Add Controller"**.
4.  Click the `🔄` refresh button to confirm its status is "● Online".

### Step 2: Add Your Bins (LEDs)
1.  On the **Settings** page, find the "Manage Bins" section.
2.  Fill out the **"Bulk Add Segment Bins"** form:
    * **WLED Controller:** Select the "Main Shelf" you just added.
    * **Segment ID:** Enter `0` (or the segment you set in WLED).
    * **Number of LEDs:** Enter the *same number* you set in WLED (e.g., `64`).
    * **Bin Name Prefix:** Enter a name (e.g., `A1-`).
3.  Click **"Add Segment Bins"**. The "Existing Bins" table will now show `A1-0`, `A1-1`, etc.

### Step 3: Add a Part to Your Catalog
1.  Navigate to the **Inventory** page.
2.  Expand the **"Add New Part Type"** form.
3.  Fill in the details for your part (e.g., Name: `220 Ohm Resistor`).
4.  Click **"Add Part Type"**.

### Step 4: Add Stock to a Bin
1.  On the **Inventory** page, click the new **"220 Ohm Resistor"** link.
2.  On the Part Details page, find the **"Add to New Bin"** section.
3.  In the "Bin" dropdown, select the bin where you are storing the part (e.g., **`A1-0 (Seg: 0, LED: 0)`**).
4.  Enter the **Quantity** (e.g., `100`).
5.  Click **"Add Stock"**.

### Step 5: Locate Your Part!
1.  Navigate back to the main **Inventory** page.
2.  Find your "220 Ohm Resistor" in the list.
3.  Click its **"Locate"** button.

The first LED on your strip (`A1-0`) should instantly light up, showing you exactly where your part is. Click **"Stop"** to turn it off.

---

## Next Steps

You're all set! Build out your inventory, impress your friends.

From here, you might be interested in:

* **[User Guide](./usage.md)** This is the detailed reference manual. It explains every feature on every page, including the Stock Dashboard, managing documents, adding categories, and more.

* **[Developer Guide](./developer.md)** This guide is for developers. It explains the application's architecture, code structure, testing philosophy, and how to contribute.