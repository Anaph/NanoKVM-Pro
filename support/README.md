# NanoKVM Support Instructions

`/support` contains auxiliary functions for NanoKVM.

## Contents

| Directory    | Description                                                          |
| ------------ | -------------------------------------------------------------------- |
| `assistant/` | CUA smart assistant: Flask web app driving the host over HDMI and HID |
| `edid/`      | EDID profiles shipped with the server, and the generator for them     |
| `scripts/`   | Image building and toolchain setup                                    |
| `tools/`     | Small host-side helpers                                               |

## How the host identifies the emulated devices

Two independent descriptors decide what the controlled machine sees:

- **The USB gadget descriptor** names the emulated keyboard, mouse and
  touchpad. All three are functions of a single composite USB device, so they
  share one vendor id, product id and set of strings. These are configurable at
  runtime under **Settings → Device → USB device identity**, which rewrites
  `/sys/kernel/config/usb_gadget/g0` and persists the choice in
  `/etc/kvm/usb-identity.json`. The boot scripts rebuild the gadget from their
  own defaults on every start, so the server re-applies the stored identity
  during startup.

  The first override snapshots the untouched descriptor into
  `/etc/kvm/usb-identity.factory.json`, which is what the `factory` preset
  restores.

- **The EDID in the LT6911 capture chip** names the "monitor". See
  [`edid/README.md`](edid/README.md).
