---
title: WLEDger Quick Start
layout: default
nav_order: 2
---

# Quick Start Guide

Welcome to WLEDger! This guide will walk you through the entire process, from setting up your hardware and installing WLEDger to finding your first part with an LED.

## Hardware Requirements

Before you begin, you'll need a few pieces of hardware.

> ℹ️ If you plan on only using the software component of WLEDger, you can skip this section - but where's the fun in that?

**For a deep dive into all your options, see the [Full Hardware Guide](./hardware-guide.md).**

### Controllers
WLEDger works with any microcontroller compatible with WLED. Here are some popular, easy-to-use options:

* **Adafruit Sparkle Motion Mini:** An all-in-one board with a level shifter, terminal block, and USB-C.
* **WEMOS D1 Mini (ESP8266):** A very common and inexpensive board.
* **ESP32 Dev Kit:** A powerful and popular board with plenty of processing power.

### LED Strips
You'll need a compatible addressable LED strip, such as:
* WS2812B (also known as NeoPixel)
* SK6812

### ⚠️ Power & Safety ⚠️

This is the most critical part of your hardware setup.

* **DO NOT** power a long LED strip (more than 10-15 LEDs) directly from your computer's USB port or the microcontroller's 5V pin. This could damage your computer or your board.
* **ALWAYS** use a separate, external 5V power supply if your WLED board does not have built-in power delivery.
* **ALWAYS use a fuse** of the appropriate size between your power supply and your LEDs to protect from shorts and excess current draw
* **Rule of Thumb:** A common rule is `60mA` (0.06A) per LED at full white brightness (peak power draw scenario). For 64 LEDs, you would need `64 * 0.06A = 3.84A`. A **5V 4A** power supply would be a good choice.
    * **Note:** This is probably a vast overbudgeting of the power requirement. The real world current draw for the "rainbow" effect on 128 LEDs in my testing setup is 1.8A. Peak current draw while using WLEDger was .5A with an idle draw of .2A. Your mileage may vary.
* **Wiring:** Connect your external 5V power supply to the LED strip's `+5V` and `GND` inputs directly. Then, connect the strip's `GND` pin to your microcontroller's `GND` pin. Finally, connect the strip's `Data` pin to your microcontroller's data pin.

> Depending on your choice of LED (WS2812B, SK6812, etc), microcontroller, and power supply, your voltage requirements may differ. That's fine, just make sure your hardware is compatible with WLED.

If LED power management isn't something you feel comfortable managing yourself, I highly recommend getting a microcontroller that is built with LED control in mind, such as the [Adafruit Sparkle Motion Mini](https://www.adafruit.com/product/6160).

This board comes with an ESP32 for running WLED, and is capable of delivering fused 5v 4A power to your project via USB-C. It's an *awesome* board.


## WLED Installation & Setup

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
1.  Using any device on your network, go to the new **IP Address** of your controller (e.g., `http://192.168.1.69`).
2.  Go to **Config > LED Preferences**.
3.  Set the **"Length"** to the total number of LEDs on your strip (e.g., `64`).
4.  Click "Save."
5.  Go back to the main WLED page. Use the color palette to turn on your lights. **Confirm they work in WLED before proceeding.**


## WLEDger Installation

Now that your hardware is working, let's run the WLEDger software.

### Install Dependencies
Make sure you have Docker installed:
* **[Docker](https://www.docker.com/get-started/)**

### Run WLEDger Using Docker

1.  **Create docker-compose.yml file:**
    ```yaml
    services:
        wledger:
            image: tuxedomakes/wledger:latest
            container_name: wledger
            restart: always
            ports:
                - "7483:3000"
        volumes:
        # IMPORTANT:
        # Change ./wledger_data to the directory where
        # you want to store your DB, img, and upload data
            - ./wledger_data:/app/data 

    volumes:
        wledger_data:

    ```
2.  **Run the container:**
    ```bash
    docker compose up -d
    ```
3.  **Access the app:**
    Open your browser to `http://localhost:3000`.


## Let's Configure WLEDger (*It's Easy!*)

With WLEDger installed, let's configure it and link your  inventory to your new hardware.

### Add Your Controller
1.  In the WLEDger UI, navigate to the **Settings** page.
2.  Under "Manage WLED Controllers," enter a **Name** (e.g., "Main Shelf") and the **IP Address** of your controller (from Section 2).
3.  Click **"Add Controller"**.
4.  Click the `🔄` refresh button to confirm its status is "● Online".

### Add Your Bins (LEDs)
1.  On the **Settings** page, find the "Manage Bins" section.
2.  Fill out the **"Bulk Add Segment Bins"** form:
    * **WLED Controller:** Select the "Main Shelf" you just added.
    * **Segment ID:** Enter `0` (or the segment you set in WLED).
    * **Number of LEDs:** Enter the *same number* you set in WLED (e.g., `64`).
    * **Bin Name Prefix:** Enter a name (e.g., `A1-`, `Tool Cabinet:`).
3.  Click **"Add Segment Bins"**. The "Existing Bins" table will now show `A1-0`, `A1-1`, etc.

### Add a Part to Your Catalog
1.  Navigate to the **Inventory** page.
2.  Expand the **"Add New Part Type"** form.
3.  Fill in the details for your part (e.g., Name: `220 Ohm Resistor`).
4.  Click **"Add Part Type"**.

### Add Stock to a Bin
1.  On the **Inventory** page, click the new **"220 Ohm Resistor"** link.
2.  On the Part Details page, find the **"Add to New Bin"** section.
3.  In the "Bin" dropdown, select the bin where you are storing the part (e.g., **`A1-0 (Seg: 0, LED: 0)`**).
4.  Enter the **Quantity** (e.g., `100`).
5.  Click **"Add Stock"**.

### Locate Your Part!
1.  Navigate back to the main **Inventory** page.
2.  Find your "220 Ohm Resistor" in the list.
3.  Click its **"Locate"** button.

The first LED on your strip (`A1-0`) should instantly light up, showing you exactly where your part is. Click **"Stop"** to turn it off.

## Next Steps

You're all set! Build out your inventory, light up the world, impress your friends.

From here, you might be interested in:

* **[User Guide](./usage.md)** This is the detailed reference manual. It explains every feature on every page, including the Stock Dashboard, managing documents, adding categories, and more.

* **[Developer Guide](./developer.md)** This guide is for developers. It explains the application's architecture, code structure, testing philosophy, and how to contribute.