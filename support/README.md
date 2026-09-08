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

  Built-in presets (selectable in that panel, or as the image default):
  `logitech-classic` (046d:c517), `logitech-mk540` (046d:c52b, Unifying
  receiver) and `logitech-mk270` (046d:c534, nano receiver) — all real Logitech
  receiver ids. The image ships `logitech-classic` as the default.

- **The EDID in the LT6911 capture chip** names the "monitor". See
  [`edid/README.md`](edid/README.md).

## Keyboard, mouse and monitor only

This firmware restricts the USB gadget to the three HID functions — keyboard,
mouse and touchpad — and nothing else. `scripts/harden_hid_only.py` patches the
`usbdev.sh` that ships in the nanokvm package (applied by `build_release.sh`) so
that `hid_start` deletes every non-HID `/boot/usb.*` flag before it reads them.

The effect is a hard guarantee at the firmware level: no USB network card (NCM
or RNDIS), microphone (UAC2), serial port (ACM), display loopback or
mass-storage drive can be enumerated on the attached host, no matter what wrote
the flag file. The web controls that used to toggle those (Settings → Device →
virtual devices, and the image-mount and microphone buttons in the menu bar)
are removed to match.

One consequence worth knowing: the USB network gadget was how a host reached the
KVM's web interface over the USB cable. With it gone, the KVM is reachable only
over Ethernet or Wi-Fi.

## Default identity: Logitech receiver + LG monitor

The image ships a disguised identity so the attached host does not see a KVM by
default. `scripts/image-overlay/` is applied by the `image` CI job (build_image
`--overlay`):

- **USB**: `/boot/usb.vid`, `usb.pid`, `usb.manufacturer`, `usb.product` and an
  empty `usb.serialnumber` describe a Logitech `046d:c517` "USB Receiver".
  usbdev.sh reads these on every boot, and the hid-only hardening leaves them
  untouched (it only removes the non-HID function flags).
- **Monitor**: a one-shot systemd unit (`nanokvm-default-edid.service`) writes
  the LG 24BK550Y-B EDID to the LT6911 once per flashed image. The chip and the
  `/etc` stamp both persist across reboots, and `GetEdid` recognises the profile
  by its marker byte, so the UI shows "LG-24BK550Y-B".

Both are the same knobs exposed at runtime under Settings; the overlay only sets
the factory default. Change or clear them there (or via `/boot/usb.*`) at any
time.

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

## Firmware images

The `image` job in `.github/workflows/build.yml` produces a flashable `.axp`.
`scripts/build_image/build_image.py` patches an existing image rather than
building a rootfs, so `BASE_IMAGE_URL` in the workflow points at a published
NanoKVM-Pro image, which supplies the kernel, bootloaders and Ubuntu userland.

Only the `nanokvm` package is installed into it. The base image already carries
`kvmcomm` and `pikvm` at the same versions, so reinstalling them changes nothing
and `pikvm`'s postinst does not survive a chroot: it calls `systemctl` and
`pikvm_init.sh`, neither of which works without a running systemd.

Building one by hand needs a few things a plain checkout does not have:

```sh
sudo apt-get install -y android-sdk-libsparse-utils qemu-user-static \
                        binfmt-support rsync
pip install tqdm

# build_image.py chroots into an arm64 rootfs; without a registered handler
# the kernel cannot exec its binaries and chroot fails with "Exec format error".
sudo update-binfmts --enable qemu-aarch64

python3 support/scripts/build_image/build_image.py base.axp \
    --app <dir with nanokvmpro_*.deb> -o firmware.axp
```

It needs root, loop devices and roughly 10 GB of free space on top of the base
image. Flash the resulting `.axp` with Sipeed's tool (see
[`scripts/build_image/README.md`](scripts/build_image/README.md)). Do not convert
it to a raw `.img` for balenaEtcher/dd: on this board's soldered eMMC that path
laid the partitions at the wrong offsets and bricked a device, recoverable only
by reflashing the `.axp` over USB.
