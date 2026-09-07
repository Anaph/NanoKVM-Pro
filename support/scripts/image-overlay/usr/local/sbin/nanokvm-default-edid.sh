#!/bin/sh
# Make the attached host see an LG 24BK550Y-B by default.
#
# The LT6911 keeps its EDID across reboots and /etc survives them too, so this
# runs once per flashed image: the stamp is only created after a successful
# write, and a boot where the EDID node is not ready yet retries next time.
set -eu

EDID=/kvmapp/server/edid/LG-24BK550Y-B.bin
DST=/proc/lt6911_info/edid
STAMP=/etc/kvm/.default-edid-applied

[ -f "$STAMP" ] && exit 0
[ -f "$EDID" ] || exit 0
[ -e "$DST" ] || exit 0

cat "$EDID" > "$DST"
mkdir -p /etc/kvm
touch "$STAMP"
