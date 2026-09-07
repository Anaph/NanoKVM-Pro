# EDID profiles

The attached host does not see the NanoKVM as a USB display. It sees whatever
EDID is loaded into the LT6911 capture chip, so the EDID decides which monitor
name, vendor and timings appear in Windows display settings, `xrandr`, or the
firmware setup of the controlled machine.

This directory holds EDID blobs that ship with the server, plus the generator
that produces them.

## Files

| File                                | Description                                        |
| ----------------------------------- | -------------------------------------------------- |
| `generate_edid.py`                  | Builds every blob in this directory from source     |
| `LG-24BK550Y-B.bin`                 | LG 24BK550Y-B, 1920x1080 @ 60 Hz                     |

## Regenerating and verifying

The blobs are committed so the firmware can ship them without a build step, but
they are fully derived from `generate_edid.py`:

```sh
python3 generate_edid.py           # rewrite the blobs
python3 generate_edid.py --check   # verify the committed blobs are up to date
```

`--check` is the useful one in review: it fails if a committed `.bin` no longer
matches what the generator produces.

## How the backend finds these profiles

`server/service/vm/edid.go` resolves a profile name in three places, in order:

1. `/kvmcomm/edid/<name>.bin` — the factory profiles that ship in the rootfs
2. `<directory of the server binary>/edid/<name>.bin` — the blobs from this directory
3. `/etc/kvm/edid/<name>` — profiles uploaded through the web UI

So installing a bundled profile means copying it next to the server binary,
alongside the `web` directory that the server already serves:

```sh
mkdir -p /kvmapp/server/edid
scp LG-24BK550Y-B.bin root@nanokvm:/kvmapp/server/edid/
```

Then pick it in **Settings → Screen → EDID**. No reboot or power cycle is
required: switching a profile writes to `/proc/lt6911_info/edid`, which the
LT6911 driver applies directly.

Alternatively, upload the same `.bin` through **Settings → Screen → EDID**
without touching the filesystem. It then lands in `/etc/kvm/edid/` and appears
under "Custom" instead of in the built-in list.

## Which profile is active

`GET /api/vm/edid` identifies the loaded profile by byte 12 of the EDID — the
low byte of the serial number — read back from
`/proc/lt6911_info/edid_snapshot`. Every bundled profile therefore needs a
unique marker in that byte, and it must not collide with the factory ones
(`0x12`, `0x30`, `0x36`, `0x38`, `0x3a`, `0x3f`). `LG-24BK550Y-B.bin` uses
`0x4c`.

Uploaded custom profiles carry no such marker, so the backend remembers the
active one in `/etc/kvm/edid/edid_flag` instead.

## About the LG 24BK550Y-B profile

The vendor id (`GSM`, Goldstar/LG), the 23.8" panel geometry, the 1920x1080 @
60 Hz preferred timing and the CEA-861 extension describe the real product.

The product code (`0x5b11`) and the serial string (`SN-24BK550Y`) are synthetic
placeholders: they vary per unit and cannot be derived from the model name. If
you need an exact match with a specific physical monitor, dump that monitor's
EDID (`cat /sys/class/drm/card*/card*-HDMI-A-1/edid > real.bin`) and upload it
as a custom profile instead.
