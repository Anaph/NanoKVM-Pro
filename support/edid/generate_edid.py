#!/usr/bin/env python3
"""Generate the EDID blobs shipped in this directory.

The NanoKVM presents itself to the attached host through the EDID stored in the
LT6911 capture chip, so the host identifies the "monitor" by whatever this blob
says.  Keeping a generator next to the binaries means the bytes stay auditable:
run this script and diff the result against the committed .bin.

    python3 generate_edid.py            # write every profile
    python3 generate_edid.py --check    # verify committed blobs match

The values below describe an LG 24BK550Y-B (23.8" 1920x1080 IPS).  The panel
geometry, timings and vendor id are those of the real product family; the
product code and serial string are synthetic placeholders, because they differ
per unit and cannot be derived from the model name alone.
"""

from __future__ import annotations

import argparse
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent


def pnp_id(letters: str) -> tuple[int, int]:
    """Encode a 3-letter PnP vendor id into the two big-endian EDID bytes."""
    if len(letters) != 3 or not letters.isalpha() or not letters.isupper():
        raise ValueError(f"invalid PnP id: {letters!r}")
    packed = 0
    for letter in letters:
        packed = (packed << 5) | (ord(letter) - ord("A") + 1)
    return (packed >> 8) & 0xFF, packed & 0xFF


def text_descriptor(tag: int, text: str) -> bytes:
    """Build an 18-byte display descriptor carrying an ASCII string."""
    payload = text.encode("ascii")
    if len(payload) > 13:
        raise ValueError(f"descriptor text too long: {text!r}")
    if len(payload) < 13:
        payload += b"\x0a" + b"\x20" * (12 - len(payload))
    return bytes([0x00, 0x00, 0x00, tag, 0x00]) + payload


def detailed_timing(
    pixel_clock_khz: int,
    h_active: int,
    h_blank: int,
    h_sync_offset: int,
    h_sync_width: int,
    v_active: int,
    v_blank: int,
    v_sync_offset: int,
    v_sync_width: int,
    h_size_mm: int,
    v_size_mm: int,
    h_sync_positive: bool = True,
    v_sync_positive: bool = True,
) -> bytes:
    """Build an 18-byte detailed timing descriptor (digital separate sync)."""
    clock = pixel_clock_khz // 10
    flags = 0x18  # digital separate sync
    if v_sync_positive:
        flags |= 0x04
    if h_sync_positive:
        flags |= 0x02

    return bytes(
        [
            clock & 0xFF,
            (clock >> 8) & 0xFF,
            h_active & 0xFF,
            h_blank & 0xFF,
            ((h_active >> 8) << 4) | (h_blank >> 8),
            v_active & 0xFF,
            v_blank & 0xFF,
            ((v_active >> 8) << 4) | (v_blank >> 8),
            h_sync_offset & 0xFF,
            h_sync_width & 0xFF,
            ((v_sync_offset & 0x0F) << 4) | (v_sync_width & 0x0F),
            ((h_sync_offset >> 8) << 6)
            | ((h_sync_width >> 8) << 4)
            | ((v_sync_offset >> 4) << 2)
            | (v_sync_width >> 4),
            h_size_mm & 0xFF,
            v_size_mm & 0xFF,
            ((h_size_mm >> 8) << 4) | (v_size_mm >> 8),
            0x00,
            0x00,
            flags,
        ]
    )


def range_limits(
    v_min: int, v_max: int, h_min: int, h_max: int, max_clock_mhz: int
) -> bytes:
    """Build the 18-byte monitor range limits descriptor (tag 0xFD)."""
    return bytes(
        [
            0x00,
            0x00,
            0x00,
            0xFD,
            0x00,
            v_min,
            v_max,
            h_min,
            h_max,
            max_clock_mhz // 10,
            0x00,
            0x0A,
        ]
    ) + b"\x20" * 6


def standard_timing(h_active: int, aspect: str, refresh: int) -> bytes:
    """Build a 2-byte standard timing entry."""
    aspects = {"16:10": 0b00, "4:3": 0b01, "5:4": 0b10, "16:9": 0b11}
    return bytes([(h_active // 8) - 31, (aspects[aspect] << 6) | (refresh - 60)])


def checksum(block: bytes) -> int:
    """The byte that makes a 128-byte block sum to zero mod 256."""
    return (-sum(block)) & 0xFF


def build_lg_24bk550y() -> bytes:
    # --- base block -------------------------------------------------------
    vendor_hi, vendor_lo = pnp_id("GSM")  # Goldstar / LG Electronics

    # The low byte of the serial doubles as the profile marker that the
    # backend uses to recognise which EDID is currently loaded, so it must
    # not collide with the factory profiles (0x12/0x30/0x36/0x38/0x3a/0x3f).
    serial_number = 0x0001014C

    base = bytearray()
    base += b"\x00\xff\xff\xff\xff\xff\xff\x00"  # header
    base += bytes([vendor_hi, vendor_lo])
    base += (0x5B11).to_bytes(2, "little")  # product code (synthetic)
    base += serial_number.to_bytes(4, "little")
    base += bytes([0x0A, 0x1B])  # week 10, year 2017
    base += bytes([0x01, 0x03])  # EDID 1.3

    base += bytes(
        [
            0x80,  # digital input
            0x35,  # 53 cm horizontal image size
            0x1E,  # 30 cm vertical image size
            0x78,  # gamma 2.2
            0x0A,  # RGB colour, preferred timing is native
        ]
    )
    # sRGB chromaticity
    base += bytes([0xEE, 0x91, 0xA3, 0x54, 0x4C, 0x99, 0x26, 0x0F, 0x50, 0x54])
    # established timings: VGA/SVGA/XGA/SXGA legacy modes
    base += bytes([0xAF, 0xCF, 0x00])

    base += standard_timing(1920, "16:9", 60)
    base += standard_timing(1680, "16:10", 60)
    base += standard_timing(1600, "16:9", 60)
    base += standard_timing(1440, "16:10", 60)
    base += standard_timing(1280, "5:4", 60)
    base += standard_timing(1280, "16:9", 60)
    base += standard_timing(1152, "4:3", 75)
    base += b"\x01\x01"  # unused

    mode_1080p60 = detailed_timing(
        pixel_clock_khz=148500,
        h_active=1920,
        h_blank=280,
        h_sync_offset=88,
        h_sync_width=44,
        v_active=1080,
        v_blank=45,
        v_sync_offset=4,
        v_sync_width=5,
        h_size_mm=527,
        v_size_mm=296,
    )

    base += mode_1080p60
    base += range_limits(v_min=56, v_max=75, h_min=30, h_max=83, max_clock_mhz=150)
    base += text_descriptor(0xFC, "24BK550Y")
    base += text_descriptor(0xFF, "SN-24BK550Y")

    base += bytes([0x01])  # one extension block
    base.append(checksum(bytes(base)))
    assert len(base) == 128, len(base)

    # --- CEA-861 extension block -----------------------------------------
    video_block = bytes(
        [
            (2 << 5) | 9,  # video data block, 9 short video descriptors
            0x80 | 16,  # 1920x1080p60 (native)
            31,  # 1920x1080p50
            4,  # 1280x720p60
            19,  # 1280x720p50
            5,  # 1920x1080i60
            20,  # 1920x1080i50
            2,  # 720x480p60
            17,  # 720x576p50
            1,  # 640x480p60
        ]
    )
    # LPCM, 2 channels, 32/44.1/48 kHz, 16/20/24 bit
    audio_block = bytes([(1 << 5) | 3, 0x09, 0x07, 0x07])
    # front left + front right
    speaker_block = bytes([(4 << 5) | 3, 0x01, 0x00, 0x00])
    # HDMI vendor block: IEEE OUI 00-0C-03, physical address 1.0.0.0
    hdmi_block = bytes([(3 << 5) | 5, 0x03, 0x0C, 0x00, 0x10, 0x00])

    data_blocks = video_block + audio_block + speaker_block + hdmi_block
    dtd_offset = 4 + len(data_blocks)

    ext = bytearray()
    ext += bytes(
        [
            0x02,  # CEA-861 extension
            0x03,  # revision 3
            dtd_offset,
            0xF1,  # underscan, audio, YCbCr 4:4:4 and 4:2:2, 1 native DTD
        ]
    )
    ext += data_blocks
    ext += mode_1080p60
    ext += detailed_timing(
        pixel_clock_khz=74250,
        h_active=1280,
        h_blank=370,
        h_sync_offset=110,
        h_sync_width=40,
        v_active=720,
        v_blank=30,
        v_sync_offset=5,
        v_sync_width=5,
        h_size_mm=527,
        v_size_mm=296,
    )
    ext += b"\x00" * (127 - len(ext))
    ext.append(checksum(bytes(ext)))
    assert len(ext) == 128, len(ext)

    return bytes(base) + bytes(ext)


PROFILES = {"LG-24BK550Y-B.bin": build_lg_24bk550y}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="verify the committed blobs instead of rewriting them",
    )
    args = parser.parse_args()

    failed = False
    for name, build in PROFILES.items():
        data = build()
        target = HERE / name

        if args.check:
            if not target.exists():
                print(f"missing: {name}")
                failed = True
            elif target.read_bytes() != data:
                print(f"stale: {name}")
                failed = True
            else:
                print(f"ok: {name}")
        else:
            target.write_bytes(data)
            print(f"wrote: {name} ({len(data)} bytes)")

    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
