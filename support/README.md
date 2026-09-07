# NanoKVM Support Instructions

`/support` contains auxiliary functions for NanoKVM.

## Contents

| Directory    | Description                                                          |
| ------------ | -------------------------------------------------------------------- |
| `assistant/` | CUA smart assistant: Flask web app driving the host over HDMI and HID |
| `edid/`      | EDID profiles shipped with the server, and the generator for them     |
| `scripts/`   | Image building, toolchain setup and release packaging                  |
| `tools/`     | Small host-side helpers                                               |

## How the host identifies the emulated devices

Two independent descriptors decide what the controlled machine sees:

- **The USB gadget descriptor** names the emulated keyboard, mouse and
  touchpad. All three are functions of a single composite USB device, so they
  share one vendor id, product id and set of strings.

  `usbdev.sh` rebuilds the gadget on every boot and reads an override file per
  field as it goes, falling back to its built-in default when one is absent:

  | File                    | Default            |
  | ----------------------- | ------------------ |
  | `/boot/usb.vid`         | `0x3346`           |
  | `/boot/usb.pid`         | `0x1009`           |
  | `/boot/usb.manufacturer`| `sipeed`           |
  | `/boot/usb.product`     | `NanoKVMPro`       |
  | `/boot/usb.serialnumber`| `0123456789ABCDEF` |

  **Settings → Device → USB device identity** writes those files and then runs
  `usbdev.sh restart` so the change takes effect immediately. Because the files
  are what the boot script reads, the override survives reboots on its own; the
  `factory` preset simply deletes them.

- **The EDID in the LT6911 capture chip** names the "monitor". See
  [`edid/README.md`](edid/README.md).

## Releases

`scripts/build_release.sh` produces the `nanokvm_pro_<version>` package layout
that `scripts/build_image/build_image.py` consumes as `--app`. CI runs it from
the `release` job in `.github/workflows/build.yml`, on a `v*` tag or a manual
run given a version.

Only the `nanokvm` package is built here — it carries `NanoKVM-Server`, the web
assets and the bundled EDID profiles. Everything else is rebased onto an
upstream release (`BASE_RELEASE_VERSION` in the workflow): `kvmcomm` and
`pikvm` are carried over untouched, and so are the parts of `nanokvm` that this
repository does not build, notably `libkvm.so`, `usbdev.sh` and the systemd
units. `server/dl_lib/kvmimpl.cpp` is only a link-time stub whose entry points
abort, so the real library has to come from upstream.

To build one locally:

```sh
./support/scripts/build_release.sh \
    --version 1.2.15+dev --base-version 1.2.15 \
    --server server/NanoKVM-Server --web web/dist --edid support/edid \
    --out dist/release
```

A full flashable `.axp` additionally needs a base image built from
[maix_ax620e_sdk](https://github.com/sipeed/maix_ax620e_sdk); see
[`scripts/build_image/README.md`](scripts/build_image/README.md).
