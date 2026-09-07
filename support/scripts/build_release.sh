#!/usr/bin/env bash
#
# Build a NanoKVM-Pro release package.
#
# The release layout this produces is the one build_image/README.md documents as
# the input to build_image.py: a nanokvm_pro_<version> directory holding one
# .deb per component plus a .json descriptor for each.
#
# Only the `nanokvm` package is built from this repository — it carries the
# server binary and the web assets. `kvmcomm` and `pikvm` contain components
# that live outside this repository (among them the real libkvm.so), so they are
# taken unchanged from an upstream release, which is also where the base
# `nanokvm` package comes from: this script swaps our freshly built files into
# it and leaves the rest of that package as it was.
#
# Usage:
#   build_release.sh --version 1.2.15+dev --base-version 1.2.15 \
#       --server dist/server/NanoKVM-Server --web dist/web --edid support/edid \
#       --out dist/release
#
set -euo pipefail

BASE_REPO="${BASE_REPO:-sipeed/NanoKVM-Pro}"
CACHE_DIR="${CACHE_DIR:-.release-cache}"

VERSION=""
BASE_VERSION=""
SERVER_BIN=""
WEB_DIR=""
EDID_DIR=""
OUT_DIR=""

die() {
    echo "[ERROR] $*" >&2
    exit 1
}

log() {
    echo "[INFO] $*"
}

usage() {
    sed -n '3,20p' "$0" | sed 's/^# \{0,1\}//'
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --base-version) BASE_VERSION="$2"; shift 2 ;;
    --server) SERVER_BIN="$2"; shift 2 ;;
    --web) WEB_DIR="$2"; shift 2 ;;
    --edid) EDID_DIR="$2"; shift 2 ;;
    --out) OUT_DIR="$2"; shift 2 ;;
    -h | --help) usage ;;
    *) die "unknown argument: $1" ;;
    esac
done

[[ -n "$VERSION" ]] || die "--version is required"
[[ -n "$BASE_VERSION" ]] || die "--base-version is required"
[[ -n "$SERVER_BIN" ]] || die "--server is required"
[[ -n "$WEB_DIR" ]] || die "--web is required"
[[ -n "$OUT_DIR" ]] || die "--out is required"

[[ -f "$SERVER_BIN" ]] || die "server binary not found: $SERVER_BIN"
[[ -d "$WEB_DIR" ]] || die "web directory not found: $WEB_DIR"
[[ -z "$EDID_DIR" || -d "$EDID_DIR" ]] || die "edid directory not found: $EDID_DIR"

for tool in dpkg-deb curl tar openssl base64; do
    command -v "$tool" >/dev/null || die "required tool not found: $tool"
done

# The package name must stay a valid Debian version, and the directory name is
# derived from it.
[[ "$VERSION" =~ ^[0-9][A-Za-z0-9.+~-]*$ ]] ||
    die "version is not a valid Debian version: $VERSION"
[[ "$BASE_VERSION" =~ ^[0-9][A-Za-z0-9.+~-]*$ ]] ||
    die "base version is not a valid Debian version: $BASE_VERSION"

SERVER_BIN="$(cd "$(dirname "$SERVER_BIN")" && pwd)/$(basename "$SERVER_BIN")"
WEB_DIR="$(cd "$WEB_DIR" && pwd)"
[[ -n "$EDID_DIR" ]] && EDID_DIR="$(cd "$EDID_DIR" && pwd)"
mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"

# --- fetch the upstream release the package is rebased onto -------------------

mkdir -p "$CACHE_DIR"
CACHE_DIR="$(cd "$CACHE_DIR" && pwd)"

base_tarball="${CACHE_DIR}/nanokvm_pro_${BASE_VERSION}.tar.gz"
if [[ ! -f "$base_tarball" ]]; then
    url="https://github.com/${BASE_REPO}/releases/download/${BASE_VERSION}/nanokvm_pro_${BASE_VERSION}.tar.gz"
    log "downloading base release: $url"
    curl -fsSL -o "${base_tarball}.part" "$url" ||
        die "failed to download the base release for ${BASE_VERSION}"
    mv "${base_tarball}.part" "$base_tarball"
else
    log "using cached base release: $base_tarball"
fi

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

log "extracting base release"
tar xzf "$base_tarball" -C "$workdir"

base_dir="${workdir}/nanokvm_pro_${BASE_VERSION}"
[[ -d "$base_dir" ]] || die "unexpected base release layout: ${base_dir} missing"

base_deb="${base_dir}/nanokvmpro_${BASE_VERSION}_arm64.deb"
[[ -f "$base_deb" ]] || die "base package not found: $base_deb"

# --- rebuild the nanokvm package ---------------------------------------------

pkg_root="${workdir}/nanokvm"
log "unpacking $(basename "$base_deb")"
dpkg-deb -R "$base_deb" "$pkg_root"

server_dir="${pkg_root}/kvmapp/server"
[[ -d "$server_dir" ]] || die "unexpected package layout: ${server_dir} missing"
[[ -f "${server_dir}/NanoKVM-Server" ]] || die "base package has no NanoKVM-Server to replace"

log "installing the freshly built server binary"
install -m 0755 "$SERVER_BIN" "${server_dir}/NanoKVM-Server"

log "installing the freshly built web assets"
rm -rf "${server_dir}/web"
mkdir -p "${server_dir}/web"
cp -a "${WEB_DIR}/." "${server_dir}/web/"

if [[ -n "$EDID_DIR" ]] && compgen -G "${EDID_DIR}/*.bin" >/dev/null; then
    log "installing bundled EDID profiles"
    mkdir -p "${server_dir}/edid"
    cp -a "${EDID_DIR}"/*.bin "${server_dir}/edid/"
fi

# The control file carries the version dpkg reports and the device's updater
# compares against.
sed -i "s/^Version: .*/Version: ${VERSION}/" "${pkg_root}/DEBIAN/control"

# conffiles and md5sums list paths whose contents just changed; dpkg-deb
# regenerates md5sums, but a stale one would make the package fail verification.
rm -f "${pkg_root}/DEBIAN/md5sums"

release_name="nanokvm_pro_${VERSION}"
release_dir="${OUT_DIR}/${release_name}"
rm -rf "$release_dir"
mkdir -p "$release_dir"

new_deb="${release_dir}/nanokvmpro_${VERSION}_arm64.deb"
log "building $(basename "$new_deb")"
dpkg-deb --root-owner-group --build "$pkg_root" "$new_deb" >/dev/null

# --- carry the untouched components over -------------------------------------

for component in kvmcomm pikvm; do
    deb="${base_dir}/${component}_${BASE_VERSION}_arm64.deb"
    if [[ -f "$deb" ]]; then
        log "carrying ${component} ${BASE_VERSION} over unchanged"
        cp -a "$deb" "$release_dir/"
        [[ -f "${base_dir}/${component}_${BASE_VERSION}.json" ]] &&
            cp -a "${base_dir}/${component}_${BASE_VERSION}.json" "$release_dir/"
    else
        echo "[WARN] ${component} not present in the base release, skipping" >&2
    fi
done

# --- descriptor for the rebuilt package --------------------------------------

# The device's updater reads these: base64 of the raw sha512 digest, and the
# exact byte size.
deb_size="$(stat -c %s "$new_deb")"
deb_sha512="$(openssl dgst -sha512 -binary "$new_deb" | base64 -w0)"

cat >"${release_dir}/nanokvmpro_${VERSION}.json" <<EOF
{
  "version": "${VERSION}",
  "name": "nanokvmpro_${VERSION}_arm64.deb",
  "sha512": "${deb_sha512}",
  "size": ${deb_size}
}
EOF

# --- package ------------------------------------------------------------------

tarball="${OUT_DIR}/${release_name}.tar.gz"
log "creating $(basename "$tarball")"
tar czf "$tarball" -C "$OUT_DIR" "$release_name"

log "release built: $tarball"
ls -la "$release_dir"
