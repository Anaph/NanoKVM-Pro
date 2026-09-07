#!/usr/bin/env python3
"""Restrict the USB gadget to keyboard, mouse and touchpad only.

usbdev.sh's hid_start builds the composite gadget from the /boot/usb.* flag
files: alongside the three HID functions it can add a USB network card (NCM or
RNDIS), a microphone (UAC2), a serial port (ACM), a display loopback and two
mass-storage LUNs (an ISO image and an SD/eMMC-backed drive). The web UI toggles
those by writing the flag files and running `usbdev.sh restart`.

This patch forces hid_start to delete every non-HID flag as its first action,
before it reads any of them. The effect is a hard guarantee: whatever the flag
files say and whoever wrote them, only the keyboard, mouse and touchpad ever
enumerate on the attached host. Nothing can be mounted or emulated beyond those.

The patch keys off the `hid_start() {` line rather than any surrounding context,
so it keeps applying if upstream changes the rest of the script, and it is
idempotent: a second run is a no-op.

Usage:
    harden_hid_only.py path/to/usbdev.sh
"""

from __future__ import annotations

import sys

MARKER = "# hid-only hardening:"

# Every /boot flag hid_start consults for a non-HID function. Kept as an
# explicit list so a reader can see exactly what is being refused.
NON_HID_FLAGS = [
    "/boot/usb.ncm",         # NCM USB network card
    "/boot/usb.rndis",       # RNDIS USB network card
    "/boot/usb.uac2",        # UAC2 microphone / audio
    "/boot/usb.udisp",       # display loopback (Loopback/SourceSink)
    "/boot/usb.acm",         # ACM serial port
    "/boot/usb.disk0",       # mass storage: mounted image / ISO
    "/boot/usb.disk1",       # mass storage: SD / eMMC drive
    "/boot/usb.disk1.sd",    # disk1 backing selector
    "/boot/usb.disk1.emmc",  # disk1 backing selector
]


def build_block() -> str:
    flags = " \\\n        ".join(NON_HID_FLAGS)
    return (
        f"    {MARKER} keyboard, mouse and touchpad only.\n"
        f"    # Remove every non-HID gadget flag before hid_start reads it, so no\n"
        f"    # network card, microphone, serial port, display or mass-storage\n"
        f"    # device can ever be added, whatever wrote the flag.\n"
        f"    rm -f {flags} 2>/dev/null || true\n"
    )


def patch(text: str) -> str:
    if MARKER in text:
        return text  # already hardened

    lines = text.splitlines(keepends=True)
    for i, line in enumerate(lines):
        if line.strip().startswith("hid_start()") and "{" in line:
            lines.insert(i + 1, build_block())
            return "".join(lines)

    raise SystemExit("hid_start() not found in usbdev.sh; refusing to patch")


def main() -> int:
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)

    path = sys.argv[1]
    with open(path, "r", encoding="utf-8") as f:
        original = f.read()

    patched = patch(original)
    if patched == original:
        print(f"{path}: already hardened")
        return 0

    with open(path, "w", encoding="utf-8") as f:
        f.write(patched)
    print(f"{path}: restricted to keyboard/mouse/touchpad only")
    return 0


if __name__ == "__main__":
    sys.exit(main())
