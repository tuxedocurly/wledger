---
title: Hardware Guide
layout: default
nav_order: 3
---

TODO: Hardware build guide is a work in progress

# Hardware Guide

WLEDger is flexible. That is, it doesn't care *what* your hardware is, as long as it runs WLED and has addressable LEDs. This guide provides an overview of the components you'll need, three common "paths" you can take to build your physical inventory setup, and the rough cost of each path.

---

## Core Components Overview

Every build will consist of these component categories.

### Microcontrollers (The "Brain")

You need a Wi-Fi-enabled microcontroller that can run the WLED firmware.

* **All-in-One (with Power Management):**
    * **Example:** [Adafruit Sparkle Motion Mini](https://www.adafruit.com/product/6314)
    * **Pros:** Easiest to use. Includes a USB-C port, a powerful ESP32, a level shifter (for clean data signals), and a terminal block for easy power/LED connection. It handles power via the USB-C port for up to 5V 4A, with a resettable fuse.
    * **Cons:** More expensive.
* **Basic & Cheap (DIY Power):**
    * **Example:** [WEMOS D1 Mini (ESP8266)](https://amzn.to/4hWNPR2), [ESP32 Dev Board](https://amzn.to/47Wq7Qh)
    * **Pros:** Extremely cheap and widely available.
    * **Cons:** Requires more wiring and know-how. You must provide your own power and (potentially) a level shifter. These are best for larger or complex setups where you're already using a custom power solution.

> Other options exist in this space, such as the [QinLED Dig series](https://www.drzzs.com/product-category/leds/). This list is not exhaustive. Choose the option that tickles your fancy.

### LED Strips

You need some addressable LEDs. The most common and well-supported type is the **WS2812B** (also sold as "NeoPixel"). You can buy them in strips, matrices, or as individual "sew-on" pixels.

Other options, such as SK6812 (and many more), are available as well.

### Storage Bins (The "Inventory")

WLEDger works by lighting up 1 or more bins when you need to locate a part. For this to work well, the storage bins you choose should be clear or semi-transparent.

* **Recommended Bin:**
    * **[High Quality 8x8 Transparent Bin Organizer](https://amzn.to/43vlFGY)**
    * This is the bin I use and what the 8-LED PCB in the `/hardware` directory is designed for. The 8x8 grid of 64 bins maps perfectly to a 64-LED strip. We'll talk more about the PCB in a moment (it's optional).
* **Alternatives:**
    * Any clear plastic storage drawers will work. You will just need to create a custom mount for your LEDs.

### Backer Board (For DIY Wiring)

If you are *not* using a custom PCB, you'll need a way to mount your individual LEDs.
* **Plywood or MDF (1/8" or 1/4"):** Rigid, easy to drill, and durable.
* **Foam Board or Cardboard:** Very cheap and easy to cut, but less durable.
* **Pegboard:** Great for larger, modular setups in a workshop.

### ⚠️ Power Supply (CRITICAL!) ⚠️

LEDs draw a lot of combines power. You **must** use an external 5V power supply or a controller with built in power management (like the Sparkle Motion or Dig series).
* **Rule of Thumb:** Budget `60mA` (0.06 Amps) per LED at full brightness when set to "white".
* **Example:** For a 64-bin setup (64 LEDs), you need `64 * 0.06A = 3.84A`.
* **Recommendation:** A **5V 4A** or **5V 10A** power supply is a safe and common choice.

> This is an overbudgeting of the power requirement under real-world circumstances, especially when using WLEDger. **This warning is meant to inform you and help keep you safe. Do your own research - your safety is on you.**
>
>Power Observations (in my setup):
>- **Peak** current draw of ~3A @ 5v for 128 bins/LEDs (rainbow effect applied in WLED)
>- **Idle** draw of .2A (LEDs off)
>- **Normal use** peak draw ~1.4A with WLEDger (locating all my parts at once using the dashboard).

---

## Path 1: The Easiest & Fastest Build (PCB + Recommended Parts)

This is the fastest, cleanest, and most "plug-and-play" method. It's designed to get you up and running with minimal soldering. It's what I'm using for my personal setup.

* **Cost:** Medium-High ($150 - $200 USD / 8x8 bin setup)
* **Time:** Low
* **Pros:** Looks professional, very fast to build, minimal wiring.
* **Cons:** Highest cost, locked into the specific bin size.

> Most of this cost is from sourcing the PCBs. If you're located **outside the United States**, your cost will likely be lower, depending on your country's import taxes and choice of PCB manufacturer.

| Component | Recommendation |
| :--- | :--- |
| **Bins** | [High Quality 8x8 Transparent Bin Organizer](https://amzn.to/43vlFGY) |
| **MCU** | [Adafruit Sparkle Motion Mini](https://www.adafruit.com/product/6160) |
| **LEDs** | [8x Custom 8-LED PCBs](/hardware/README.md) (Order from JLCPCB, etc.) |
| **Backer** | A sheet of cardboard, MDF, or thin plywood to attach the PCBs to. |
| **Power** | 5V 4A+ Power Supply (USB brick, battery pack, etc) |
| **Wiring** | Wire (22 AWG is good) |

---

## Path 2: The "Cheapest" Build (DIY Wiring)

This is the classic DIY method. It's cheap to build, but requires the most time. You will cut an LED strip into 64 individual pieces and solder them all back together.

* **Cost:** Low ($70 USD / 8x8 bin setup)
* **Time:** High
* **Pros:** Very cheap, great soldering practice, can fit any bin spacing requirement.
* **Cons:** Time-consuming and tedious.

>**Fun Fact:** If using an 8x8 storage container and WS2812B LEDs, you'll need to cut, strip, and solder 384 wires and connection points. Weeeeeee!

| Component | Recommendation |
| :--- | :--- |
| **Bins** | [High Quality 8x8 Transparent Bin Organizer](https://amzn.to/43vlFGY) |
| **MCU** | [WEMOS D1 Mini](https://amzn.to/4hWNPR2) (or [ESP32 Dev Board](https://amzn.to/47Wq7Qh)) |
| **LEDs** | 2 meters of WS2812B (60 LED/m) Strip, or 64 individual pixels|
| **Backer** | A sheet of cardboard, MDF, or thin plywood to stick the LEDs to. |
| **Power** | 5V 4A+ Power Supply |
| **Wiring** | Wire (22 AWG is good) |

**Process:**
1.  Cut your backer board to fit the back of the bin cabinet.
2.  Cut your LED strip into 64 individual pixels.
3.  Arrange and glue the 64 pixels onto the backer board.
4.  Painstakingly solder 3 wires (`+5V`, `GND`, `Data`) from one pixel to the next, following the `D_OUT` to `D_IN` flow.

---

## Path 3: The Choose Your Own Adventure Build (Workshop, Ikea Shelves, etc)

WLEDger is not just for small part bins. You can use it for *any* storage you want to light up.

The only rule is: **If WLED can control it, WLEDger can find it.**

* **IKEA KALLAX:** Put a short LED strip in each cube. Use WLEDger to track which cube holds your favorite books, 3D printer filament, or test equipment.
* **Garage Workshop:** Line your tool drawers with LED strips. Assign `Segment 0` to your screwdriver drawer and `Segment 1` to your wrench drawer.
* **Pegboard:** Attach LED pixels behind your most-used tools.

The possibilities are endless.

Have questions? Want to show off your setup? Have a suggestion for a feature? Join the [TuxedoDevices Discord](https://discord.com/invite/HABg37gjrd) and come say hi!