## Hardware Designs for WLEDger

This directory contains open-source hardware (KiCad) files for custom PCBs to help you save time building the physical side of your WLEDger inventory system. It is completely optional (but saves a LOT of assembly time).

### This is Completely Optional!

You **do not** need to fabricate this PCB to use WLEDger. The app works with **any** WLED-compatible setup.

This PCB is simply a **time-saver** and a way to get a cleaner, more robust, and more professional-looking final build without the hassle of hand-wiring 64 individual LEDs.

### What's In This Directory?

* **`8-LED-Strip`**: KiCad project files (`.kicad_pro`, `.kicad_sch`, `.kicad_pcb`) for a simple 8-LED strip.
* **`Gerbers`**: Manufacturing files you can upload directly to a manufacturer, along with a BOM and position file.

### How to Use These Files

1.  **Software:** You will need [KiCad](https://www.kicad.org/), a free and open-source electronics design suite, to open and edit these files.
2.  **Modify:** Open the `.kicad_pro` project to modify the schematic (`.kicad_sch`) and/or PCB layout (`.kicad_pcb`) to fit your needs.
3.  **Fabricate:** You can export the Gerber files from KiCad and send them to any PCB fabrication service (like JLCPCB, PCBWay, OSH Park, etc.) to have them professionally made.

---

### 8-LED PCB "Strip" for 8x8 Bins

This PCB is a simple, chainable 8-LED strip utilizing 5v SK6812 RBG LEDs with a .1uF capacitor on each LED.

#### Purpose & Design

This design was specifically created to fit the spacing requirements of this high quality **[8x8 transperent storage bin organizer](https://amzn.to/4oPHY2U)**.

The PCB is dimensioned to fit behind one row of 8 bins. You can fabricate 8 of these identical strips and chain them (using the `D_IN` and `D_OUT` pads, along with `5V` and `GND` pads) to create a single 64-LED WLED segment that maps perfectly to the 64-bin organizer.

This serves as a **design blueprint**. You are highly encouraged to open the KiCad files and modify them to fit your own specific bin setups or storage containers. The design is simple, so it's a great opportunity to learn a new skill if you aren't already familiar with using EDA software!

#### The DIY Alternative

The WLEDger system works perfectly well by hand-wiring individual LEDs. This is a common and fully supported method:

1.  Get a standard LED strip (like a WS2812B "NeoPixel" strip).
2.  Cut the strip into single-LED segments.
3.  Space the LEDs to match the layout of your bins and stick them onto a backer board (cardboard, plywood... your wall, etc. Get freaky with it)
4.  Solder short wires (e.g., 2-3 inches) to the `+5V`, `GND`, and `D_IN`/`D_OUT` pads to chain them together.

## ❗ Bounty Alert ❗
**If you find an LED strip + storage bin combination whose spacing does not require cutting the LEDs**, please let me know! This would be a huge win for simplicity, and the community. I'll even add your name to the credits for this project (if desired). 

You can reach me @TuxedoMakes on the [TuxedoDevices Discord](https://discord.com/invite/HABg37gjrd), submit an issue, or a create a pull request! 🫶
