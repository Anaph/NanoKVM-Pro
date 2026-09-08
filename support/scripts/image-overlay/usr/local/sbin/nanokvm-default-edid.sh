#!/bin/sh
# Make the attached host see an LG 24BK550Y-B by default.
#
# The EDID node is not ready the instant this unit starts: the LT6911 driver and
# the capture app bring it up a little later. The earlier version gave up on the
# first boot when the node was missing and never came back, which is why the host
# still saw the factory (SPD) monitor. This version waits for the node, writes
# the profile, and confirms it took by reading back the snapshot marker byte,
# retrying until it succeeds. It runs every boot (no stamp): re-applying is the
# same chip write the factory reset does, and it self-heals if the chip was
# changed at runtime.
set -u

EDID=/kvmapp/server/edid/LG-24BK550Y-B.bin
DST=/proc/lt6911_info/edid
SNAP=/proc/lt6911_info/edid_snapshot
# Byte 12 of the profile (low byte of the serial) is what GetEdid matches on;
# 0x4c identifies LG-24BK550Y-B.
MARKER=4c
ATTEMPTS=30
DELAY=2

log() { echo "nanokvm-default-edid: $*"; }

[ -f "$EDID" ] || { log "profile not found: $EDID"; exit 0; }

i=0
while [ "$i" -lt "$ATTEMPTS" ]; do
	if [ -e "$DST" ]; then
		if cat "$EDID" > "$DST" 2>/dev/null; then
			# Give the driver a moment to refresh the snapshot.
			sleep "$DELAY"
			if [ -r "$SNAP" ]; then
				cur=$(dd if="$SNAP" bs=1 skip=12 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n')
				if [ "$cur" = "$MARKER" ]; then
					log "applied and confirmed (marker $cur)"
					exit 0
				fi
				log "written, marker is ${cur:-none}, expected $MARKER; retrying"
			else
				# No snapshot to verify against; the write returned success.
				log "applied (snapshot not readable to confirm)"
				exit 0
			fi
		else
			log "write to $DST failed; retrying"
		fi
	fi
	i=$((i + 1))
	sleep "$DELAY"
done

log "gave up after $ATTEMPTS attempts; leaving the current EDID in place"
exit 0
