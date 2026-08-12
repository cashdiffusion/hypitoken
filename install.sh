#!/usr/bin/env bash
#
# CPA-Claude installer
# -----------------------------------------------------------------------------
# Installs or upgrades the hypitoken binary from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/hypit-ai/hypitoken/main/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/hypit-ai/hypitoken/main/install.sh | bash -s -- --version v0.1.0
#   curl -fsSL https://raw.githubusercontent.com/hypit-ai/hypitoken/main/install.sh | bash -s -- --prefix ~/.local
#
# Options:
#   --version <tag>   Install this exact tag (default: latest release).
#   --prefix  <dir>   Install prefix; binary goes to <dir>/bin (default: /usr/local).
#   --bin-dir <dir>   Override binary directory directly.
#   --mirror  <url>   Prefix for github.com / raw.githubusercontent.com URLs.
#                     Defaults to https://gh-proxy.com/ (works worldwide; avoids
#                     unreliable direct access from China). Pass --mirror direct
#                     (or CPA_MIRROR=direct) to disable and hit GitHub directly.
#
# Environment variables (same as flags, flags win):
#   CPA_VERSION, CPA_PREFIX, CPA_BIN_DIR, CPA_MIRROR, GITHUB_TOKEN
# -----------------------------------------------------------------------------

set -euo pipefail

# ===========================================================================
# Constants
# ===========================================================================
REPO="hypit-ai/hypitoken"
BIN_NAME="hypitoken"

# ===========================================================================
# Helper functions  (all defined before any calls — order-safe)
# ===========================================================================
msg()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!!\033[0m %s\n' "$*" >&2; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || err "missing dependency: $1"; }

# Wrap a github.com / raw.githubusercontent.com URL with the mirror prefix.
# We strip the leading "https://" before concatenating because mirrors like
# gh-proxy.com only reliably accept the schemeless form
#   https://gh-proxy.com/github.com/<owner>/<repo>/...
# The double-scheme form (gh-proxy.com/https://github.com/...) sometimes
# works for tiny raw files but routinely fails for release binaries.
gh_url() {
  if [ -n "$MIRROR" ]; then
    printf '%s%s' "$MIRROR" "${1#https://}"
  else
    printf '%s' "$1"
  fi
}

# Run a command with sudo when not root, plain when root.
run_privileged() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    err "need root or sudo to run: $*"
  fi
}

# Default mirror used unless the caller overrode MIRROR (via --mirror or
# CPA_MIRROR). gh-proxy.com works from both China and the rest of the world,
# so it's safe as an unconditional default. To opt out and hit GitHub
# directly, pass --mirror direct (or CPA_MIRROR=direct).
readonly DEFAULT_MIRROR="https://gh-proxy.com/"

# apply_default_mirror picks a mirror when the user didn't set one, and
# converts the sentinel "direct" / "none" into "no mirror".
apply_default_mirror() {
  case "$MIRROR" in
    direct|none|off)
      MIRROR=""
      return
      ;;
    "")
      MIRROR="$DEFAULT_MIRROR"
      ;;
  esac
}

download() {
  local out="$1" url="$2"
  # url is already mirror-prefixed by gh_url at the call site — do not
  # re-apply MIRROR here (that would produce gh-proxy.com/gh-proxy.com/...).
  auth_curl -fsSL --connect-timeout 15 --max-time 300 -o "$out" "$url"
}

# Prompt the user interactively (reads from /dev/tty for curl-pipe compat).
ask() {
  local prompt="$1" default="$2" reply=""
  if [ ! -r /dev/tty ]; then
    printf '%s' "$default"
    return
  fi
  printf '\033[1;36m?\033[0m %s [%s]: ' "$prompt" "$default" > /dev/tty
  IFS= read -r reply < /dev/tty || reply=""
  [ -z "$reply" ] && reply="$default"
  printf '%s' "$reply"
}

# Write a systemd unit file for hypitoken.
write_unit() {
  local cfg="$1" user="$2" workdir tmp_unit
  workdir="$(getent passwd "$user" | cut -d: -f6)"
  # A system user's passwd entry names a home directory that useradd
  # --no-create-home never actually created. systemd then kills the unit at
  # CHDIR (status=200) before the binary runs — that silently disabled the
  # off-host backup on one deployment for 20 days, 20 failed runs, 0 uploads.
  # Test for the directory, not just for an empty field.
  [ -d "$workdir" ] || workdir="/"
  tmp_unit="$(mktemp)"
  cat > "$tmp_unit" <<UNIT
[Unit]
Description=CPA-Claude relay
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${user}
WorkingDirectory=${workdir}
ExecStart=${BIN_DIR}/${BIN_NAME} --config ${cfg}
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT
  run_privileged install -m 0644 "$tmp_unit" "$UNIT_PATH"
  rm -f "$tmp_unit"
}

# write_backup_units installs the daily off-host backup timer + oneshot service.
# Mirrors write_unit; the oneshot runs `<bin> backup --config <cfg>` (a clean
# no-op until backup.enabled is set in the config).
BACKUP_SERVICE="hypitoken-backup.service"
BACKUP_TIMER="hypitoken-backup.timer"
write_backup_units() {
  local cfg="$1" user="$2" workdir tmp_s tmp_t
  workdir="$(getent passwd "$user" | cut -d: -f6)"
  # A system user's passwd entry names a home directory that useradd
  # --no-create-home never actually created. systemd then kills the unit at
  # CHDIR (status=200) before the binary runs — that silently disabled the
  # off-host backup on one deployment for 20 days, 20 failed runs, 0 uploads.
  # Test for the directory, not just for an empty field.
  [ -d "$workdir" ] || workdir="/"
  tmp_s="$(mktemp)"; tmp_t="$(mktemp)"
  cat > "$tmp_s" <<UNIT
[Unit]
Description=hypitoken off-host backup (oneshot)
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
User=${user}
WorkingDirectory=${workdir}
ExecStart=${BIN_DIR}/${BIN_NAME} backup --config ${cfg}
NoNewPrivileges=true
PrivateTmp=true
UNIT
  cat > "$tmp_t" <<UNIT
[Unit]
Description=Daily hypitoken off-host backup

[Timer]
OnCalendar=*-*-* 03:30:00
RandomizedDelaySec=900
Persistent=true

[Install]
WantedBy=timers.target
UNIT
  run_privileged install -m 0644 "$tmp_s" "/etc/systemd/system/${BACKUP_SERVICE}"
  run_privileged install -m 0644 "$tmp_t" "/etc/systemd/system/${BACKUP_TIMER}"
  rm -f "$tmp_s" "$tmp_t"
}

# ensure_backup_timer writes the units and enables the timer (idempotent).
ensure_backup_timer() {
  local cfg="$1" user="$2"
  [ -z "$cfg" ] || [ -z "$user" ] && return 0
  msg "installing daily backup timer (${BACKUP_TIMER})"
  write_backup_units "$cfg" "$user"
  run_privileged systemctl daemon-reload || true
  run_privileged systemctl enable --now "$BACKUP_TIMER" \
    || warn "could not enable $BACKUP_TIMER -- enable manually: sudo systemctl enable --now $BACKUP_TIMER"
}

# ===========================================================================
# Argument parsing
# ===========================================================================
VERSION="${CPA_VERSION:-latest}"
PREFIX="${CPA_PREFIX:-/usr/local}"
BIN_DIR="${CPA_BIN_DIR:-}"
MIRROR="${CPA_MIRROR:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --prefix)  PREFIX="$2";  shift 2 ;;
    --bin-dir) BIN_DIR="$2"; shift 2 ;;
    --mirror)  MIRROR="$2";  shift 2 ;;
    --force)   CPA_FORCE=1;  shift ;;
    -h|--help)
      sed -n '3,20p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

[ -z "$BIN_DIR" ] && BIN_DIR="${PREFIX%/}/bin"

# ===========================================================================
# Main
# ===========================================================================
need curl
need tar

apply_default_mirror

# Normalize mirror: ensure trailing slash.
if [ -n "$MIRROR" ]; then
  case "$MIRROR" in */) ;; *) MIRROR="${MIRROR}/" ;; esac
fi

# ---- detect OS / ARCH ----
OS="$(uname -s)"
case "$OS" in
  Linux)  OS_TAG=linux ;;
  Darwin) OS_TAG=darwin ;;
  *) err "unsupported OS: $OS (use Windows zip manually from Releases)" ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH_TAG=amd64 ;;
  aarch64|arm64) ARCH_TAG=arm64 ;;
  *) err "unsupported arch: $ARCH" ;;
esac

# ---- resolve tag ----
auth_header=""
[ -n "${GITHUB_TOKEN:-}" ] && auth_header="Authorization: Bearer $GITHUB_TOKEN"

# Helper: curl with optional auth header (avoids empty-array issues with set -u).
auth_curl() {
  if [ -n "$auth_header" ]; then
    curl -H "$auth_header" "$@"
  else
    curl "$@"
  fi
}

if [ "$VERSION" = "latest" ]; then
  msg "resolving latest release..."
  # Tag resolution goes direct to api.github.com, never through the mirror:
  # common mirrors (e.g. gh-proxy.com) refuse api.github.com and return 403,
  # and the github.com /releases/latest HTML redirect is also blocked as
  # "web page content". The API response is tiny, so a direct hit is fine
  # even from mirror-land. Mirror is reserved for the actual binary download.
  API_URL="https://api.github.com/repos/${REPO}/releases/latest"
  TAG="$(auth_curl -fsSL --connect-timeout 15 --max-time 30 "$API_URL" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1 || true)"
  [ -n "$TAG" ] || err "could not resolve latest tag via api.github.com (try --version vX.Y.Z)"
else
  TAG="$VERSION"
fi
msg "installing $REPO@$TAG -> $BIN_DIR/$BIN_NAME ($OS_TAG/$ARCH_TAG)"
[ -n "$MIRROR" ] && msg "using mirror: $MIRROR"

# Short-circuit: if the binary on disk is already at $TAG, skip the
# download/extract/install sequence and just restart the service (when
# one exists). Users re-running the installer as a quick "restart the
# running service after config changes" shortcut get that behavior for
# free. Set CPA_FORCE=1 (or pass --force) to bypass and reinstall.
# --version format: "hypitoken vX.Y.Z (commit=... built=...)"
if [ "${CPA_FORCE:-}" != "1" ] && [ -x "$BIN_DIR/$BIN_NAME" ]; then
  CURRENT_TAG="$("$BIN_DIR/$BIN_NAME" --version 2>/dev/null \
    | awk '{print $2}' | head -n1 || true)"
  if [ -n "$CURRENT_TAG" ] && [ "$CURRENT_TAG" = "$TAG" ]; then
    msg "already at $TAG — skipping download"
    if [ "$OS_TAG" = "linux" ] && command -v systemctl >/dev/null 2>&1 \
         && [ -f "/etc/systemd/system/hypitoken.service" ]; then
      msg "restarting hypitoken.service"
      run_privileged systemctl restart hypitoken.service \
        || warn "restart failed; check: systemctl status hypitoken.service"
      run_privileged systemctl --no-pager --lines=0 status hypitoken.service \
        || true
    else
      msg "no systemd unit found — nothing to restart"
    fi
    exit 0
  fi
fi

# Tag is like "v0.1.0"; GoReleaser archive strips the leading "v".
TRIMMED="${TAG#v}"
ASSET="hypitoken_${TRIMMED}_${OS_TAG}_${ARCH_TAG}.tar.gz"
URL="$(gh_url "https://github.com/${REPO}/releases/download/${TAG}/${ASSET}")"
SUM_URL="$(gh_url "https://github.com/${REPO}/releases/download/${TAG}/checksums.txt")"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

msg "downloading $URL"
download "$TMP/$ASSET" "$URL" \
  || err "download failed; check tag/asset name, or try --mirror direct"

msg "verifying checksum"
if auth_curl -fsSL --connect-timeout 15 --max-time 60 \
     -o "$TMP/checksums.txt" "$SUM_URL"; then
  expected="$(grep " ${ASSET}\$" "$TMP/checksums.txt" | awk '{print $1}')"
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
    else
      actual="$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')"
    fi
    [ "$actual" = "$expected" ] || err "checksum mismatch ($actual != $expected)"
  else
    warn "no checksum entry for $ASSET -- skipping verification"
  fi
else
  warn "could not fetch checksums.txt -- skipping verification"
fi

msg "extracting"
tar -xzf "$TMP/$ASSET" -C "$TMP"
[ -f "$TMP/$BIN_NAME" ] || err "extracted archive does not contain $BIN_NAME"

# ---- detect existing systemd unit (for upgrade path) ----
UNIT_NAME="hypitoken.service"
UNIT_PATH="/etc/systemd/system/${UNIT_NAME}"
UNIT_EXISTS=0
UNIT_WAS_ACTIVE=0
if [ "$OS_TAG" = "linux" ] && command -v systemctl >/dev/null 2>&1 && [ -f "$UNIT_PATH" ]; then
  UNIT_EXISTS=1
  if systemctl is-active --quiet "$UNIT_NAME"; then
    UNIT_WAS_ACTIVE=1
  fi
fi

# ---- install binary ----
if [ -w "$BIN_DIR" ] || { [ ! -d "$BIN_DIR" ] && [ -w "$(dirname "$BIN_DIR")" ]; }; then
  mkdir -p "$BIN_DIR"
  install -m 0755 "$TMP/$BIN_NAME" "$BIN_DIR/$BIN_NAME"
else
  msg "sudo needed for $BIN_DIR"
  run_privileged mkdir -p "$BIN_DIR"
  run_privileged install -m 0755 "$TMP/$BIN_NAME" "$BIN_DIR/$BIN_NAME"
fi

msg "installed:"
"$BIN_DIR/$BIN_NAME" --version || true

# ---- systemd integration ----
if [ "$UNIT_EXISTS" = "1" ]; then
  # Upgrade path: keep existing unit, just reload + restart if it was running.
  msg "existing systemd unit detected at $UNIT_PATH -- preserving it"
  run_privileged systemctl daemon-reload || true
  # Ensure the daily backup timer exists too, deriving cfg+user from the main
  # unit so it stays in lockstep with the service definition.
  _bcfg="$(systemctl cat "$UNIT_NAME" 2>/dev/null | sed -nE 's/^ExecStart=.* -{1,2}config[ =]([^ ]+).*/\1/p' | head -1)"
  _buser="$(systemctl cat "$UNIT_NAME" 2>/dev/null | sed -nE 's/^User=(.+)/\1/p' | head -1)"
  ensure_backup_timer "$_bcfg" "$_buser"
  if [ "$UNIT_WAS_ACTIVE" = "1" ]; then
    msg "restarting $UNIT_NAME to pick up the new binary"
    run_privileged systemctl restart "$UNIT_NAME" \
      || warn "restart failed; check: systemctl status $UNIT_NAME"
  else
    msg "unit is installed but not active -- start it with: sudo systemctl start $UNIT_NAME"
  fi
elif [ "$OS_TAG" = "linux" ] && command -v systemctl >/dev/null 2>&1 && [ -r /dev/tty ]; then
  RUN_USER="$(id -un)"
  DEFAULT_CFG="$HOME/.config/hypitoken/config.yaml"
  reply="$(ask "Create systemd service ${UNIT_NAME} running as '${RUN_USER}'? (y/N)" "N")"
  case "$reply" in
    y|Y|yes|YES)
      CFG_PATH="$(ask "Config file path" "$DEFAULT_CFG")"

      CFG_DIR="$(dirname "$CFG_PATH")"
      mkdir -p "$CFG_DIR" 2>/dev/null || run_privileged mkdir -p "$CFG_DIR"
      if [ ! -f "$CFG_PATH" ]; then
        CFG_URL="$(gh_url "https://raw.githubusercontent.com/${REPO}/${TAG}/config.example.yaml")"
        msg "fetching config.example.yaml -> $CFG_PATH"
        if auth_curl -fsSL --connect-timeout 15 --max-time 60 \
             -o "$TMP/config.yaml" "$CFG_URL"; then
          if [ -w "$CFG_DIR" ]; then
            install -m 0640 "$TMP/config.yaml" "$CFG_PATH"
          else
            run_privileged install -m 0640 -o "$RUN_USER" -g "$RUN_USER" "$TMP/config.yaml" "$CFG_PATH"
          fi
        else
          warn "could not fetch example config -- you must create $CFG_PATH manually"
        fi
      else
        msg "config already exists at $CFG_PATH -- leaving it alone"
      fi

      msg "writing $UNIT_PATH"
      write_unit "$CFG_PATH" "$RUN_USER"
      run_privileged systemctl daemon-reload
      ensure_backup_timer "$CFG_PATH" "$RUN_USER"

      cat <<EOF

Systemd unit installed. Next:

  1. Edit $CFG_PATH (set access_tokens, OAuth files in auth_dir).
  2. sudo systemctl enable --now $UNIT_NAME
  3. sudo systemctl status $UNIT_NAME
     journalctl -u $UNIT_NAME -f

Re-running this installer later will upgrade the binary and auto-restart the service if it was running.
EOF
      exit 0
      ;;
  esac
fi

cat <<EOF

Next steps:

  1. curl -fsSL https://raw.githubusercontent.com/${REPO}/${TAG}/config.example.yaml -o config.yaml
     (edit config.yaml -- at minimum set access_tokens and add OAuth files to auth_dir)

  2. $BIN_NAME --config config.yaml

Upgrade later by re-running this installer (it overwrites the binary in place;
if a hypitoken.service exists and is running, it will be auto-restarted).
EOF
