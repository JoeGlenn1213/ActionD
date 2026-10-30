#!/usr/bin/env bash
# Fixture tests for scripts/autostart.sh Homebrew detection.
# Stubs launchctl/systemctl. Does not bootstrap a real agent or talk to brew services.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="${ROOT}/scripts/autostart.sh"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    if [[ -n "${OUT:-}" ]]; then
        printf '%s\n' "--- output ---" "$OUT" "--- end ---" >&2
    fi
    exit 1
}

assert_ok() {
    local status=0
    OUT="$("$@" 2>&1)" || status=$?
    [[ "$status" -eq 0 ]] || fail "expected exit 0, got ${status}: $*"
}

assert_fail() {
    local status=0
    OUT="$("$@" 2>&1)" || status=$?
    [[ "$status" -ne 0 ]] || fail "expected non-zero exit: $*"
}

assert_contains() {
    local needle="$1"
    [[ "$OUT" == *"$needle"* ]] || fail "missing [${needle}]"
}

assert_not_contains() {
    local needle="$1"
    [[ "$OUT" != *"$needle"* ]] || fail "unexpected [${needle}]"
}

make_env() {
    WORK="$(mktemp -d)"
    export HOME="${WORK}/home"
    export BIN="${WORK}/bin"
    export SYSTEMCTL_STATE="${WORK}/systemctl"
    export LAUNCHCTL_LIST="${WORK}/launchctl-list"
    export AUTOSTART_LAUNCH_DAEMON_DIR="${WORK}/LaunchDaemons"
    export AUTOSTART_SYSTEMD_SYSTEM_DIR="${WORK}/systemd-system"
    mkdir -p "$HOME" "$BIN" "$SYSTEMCTL_STATE" "$AUTOSTART_LAUNCH_DAEMON_DIR" "$AUTOSTART_SYSTEMD_SYSTEM_DIR"
    : >"$LAUNCHCTL_LIST"
    cat >"${BIN}/lgh" <<'EOF'
#!/bin/sh
exit 0
EOF
    cat >"${BIN}/actiond" <<'EOF'
#!/bin/sh
exit 0
EOF
    cat >"${BIN}/launchctl" <<'EOF'
#!/bin/bash
if [[ "${1:-}" == "list" ]]; then
    cat "${LAUNCHCTL_LIST}"
    exit 0
fi
exit 0
EOF
    cat >"${BIN}/systemctl" <<'EOF'
#!/bin/bash
args=("$@")
i=0
if [[ "${args[0]:-}" == "--user" ]]; then
    i=1
fi
action="${args[$i]:-}"
unit="${args[$((i + 1))]:-}"
case "$action" in
    is-active)
        state="$(cat "${SYSTEMCTL_STATE}/${unit}.active" 2>/dev/null || echo inactive)"
        printf '%s\n' "$state"
        [[ "$state" == "active" ]]
        ;;
    is-enabled)
        state="$(cat "${SYSTEMCTL_STATE}/${unit}.enabled" 2>/dev/null || echo disabled)"
        printf '%s\n' "$state"
        [[ "$state" == "enabled" ]]
        ;;
    show)
        printf '%s\n' "${SYSTEMCTL_MAINPID:-4321}"
        ;;
    *)
        exit 0
        ;;
esac
EOF
    chmod +x "${BIN}/lgh" "${BIN}/actiond" "${BIN}/launchctl" "${BIN}/systemctl"
    export PATH="${BIN}:${PATH}"
}

cleanup() {
    if [[ -n "${WORK:-}" && -d "${WORK:-}" ]]; then
        rm -rf "$WORK"
    fi
}
trap cleanup EXIT

bash -n "$SCRIPT"

# --- Linux: legacy unit file skips install, status reports it ---
make_env
export AUTOSTART_PLATFORM=Linux
mkdir -p "${HOME}/.config/systemd/user"
printf '%s\n' '[Service]' 'ExecStart=/opt/homebrew/bin/actiond start' \
    >"${HOME}/.config/systemd/user/homebrew.actiond.service"
assert_ok "$SCRIPT" install --dry-run
assert_contains "Skipping ActionD"
assert_contains "homebrew.actiond"
assert_contains "--ignore-homebrew"
assert_contains "Would write:"
assert_contains "lgh.service"
# The "Would write" line for our actiond unit must not appear. lgh.service is ours.
if [[ "$OUT" == *"Would write:"* && "$OUT" == *"${HOME}/.config/systemd/user/actiond.service"* ]]; then
    fail "dry-run still planned to write our actiond unit"
fi
[[ ! -e "${HOME}/.config/systemd/user/actiond.service" ]] || fail "dry-run created actiond unit"
[[ -e "${HOME}/.config/systemd/user/homebrew.actiond.service" ]] || fail "brew unit was removed"
assert_ok "$SCRIPT" status
assert_contains "Homebrew: systemd user unit homebrew.actiond"
assert_contains "not installed"
cleanup
WORK=""

# --- Linux: canonical name, enabled but not active, skips ---
make_env
export AUTOSTART_PLATFORM=Linux
printf 'enabled\n' >"${SYSTEMCTL_STATE}/sh.brew.lgh.service.enabled"
assert_ok "$SCRIPT" install --dry-run
assert_contains "Skipping LGH"
assert_contains "sh.brew.lgh"
assert_not_contains "Skipping ActionD"
assert_contains "actiond.service"
cleanup
WORK=""

# --- Linux: active canonical unit without a unit file is still managed ---
make_env
export AUTOSTART_PLATFORM=Linux
export SYSTEMCTL_MAINPID=77
printf 'active\n' >"${SYSTEMCTL_STATE}/sh.brew.actiond.service.active"
assert_ok "$SCRIPT" status
assert_contains "sh.brew.actiond"
assert_contains "running PID 77"
assert_ok "$SCRIPT" install --dry-run
assert_contains "Skipping ActionD"
cleanup
WORK=""

# --- Linux: --ignore-homebrew previews our unit and warns ---
make_env
export AUTOSTART_PLATFORM=Linux
mkdir -p "${HOME}/.config/systemd/user"
: >"${HOME}/.config/systemd/user/homebrew.lgh.service"
assert_ok "$SCRIPT" install --dry-run --ignore-homebrew
assert_contains "--ignore-homebrew is set"
assert_contains "Would write: ${HOME}/.config/systemd/user/lgh.service"
assert_not_contains "Skipping LGH"
cleanup
WORK=""

# --- Linux: brew file plus our unit, override still refuses without --force ---
make_env
export AUTOSTART_PLATFORM=Linux
mkdir -p "${HOME}/.config/systemd/user"
: >"${HOME}/.config/systemd/user/homebrew.lgh.service"
printf '%s\n' '[Service]' 'ExecStart=/tmp/lgh serve' >"${HOME}/.config/systemd/user/lgh.service"
assert_fail "$SCRIPT" install --dry-run --ignore-homebrew
assert_contains "Existing service files"
cleanup
WORK=""

# --- Linux: system unit path (sudo brew services) ---
make_env
export AUTOSTART_PLATFORM=Linux
: >"${AUTOSTART_SYSTEMD_SYSTEM_DIR}/homebrew.actiond.service"
assert_ok "$SCRIPT" install --dry-run
assert_contains "Skipping ActionD"
assert_contains "${AUTOSTART_SYSTEMD_SYSTEM_DIR}/homebrew.actiond.service"
cleanup
WORK=""

# --- Linux: uninstall removes only our unit ---
make_env
export AUTOSTART_PLATFORM=Linux
mkdir -p "${HOME}/.config/systemd/user"
printf '%s\n' '[Service]' 'ExecStart=/tmp/lgh serve' >"${HOME}/.config/systemd/user/lgh.service"
printf '%s\n' '[Service]' 'ExecStart=/opt/homebrew/bin/lgh serve' \
    >"${HOME}/.config/systemd/user/homebrew.lgh.service"
assert_ok "$SCRIPT" uninstall
[[ ! -e "${HOME}/.config/systemd/user/lgh.service" ]] || fail "our unit was kept"
[[ -e "${HOME}/.config/systemd/user/homebrew.lgh.service" ]] || fail "brew unit was removed"
assert_contains "Left Homebrew-managed LGH"
cleanup
WORK=""

# --- Linux: nothing from Homebrew still previews both units ---
make_env
export AUTOSTART_PLATFORM=Linux
assert_ok "$SCRIPT" install --dry-run
assert_contains "Would write: ${HOME}/.config/systemd/user/lgh.service"
assert_contains "Would write: ${HOME}/.config/systemd/user/actiond.service"
assert_not_contains "Skipping"
assert_ok "$SCRIPT" status
assert_contains "Homebrew: not registered"
cleanup
WORK=""

# --- macOS: legacy LaunchAgent plist skips, status reports it ---
make_env
export AUTOSTART_PLATFORM=Darwin
mkdir -p "${HOME}/Library/LaunchAgents"
: >"${HOME}/Library/LaunchAgents/homebrew.mxcl.lgh.plist"
assert_ok "$SCRIPT" install --dry-run
assert_contains "Skipping LGH"
assert_contains "homebrew.mxcl.lgh"
assert_contains "--ignore-homebrew"
assert_contains "Would write:"
assert_contains "com.localgithub.actiond.plist"
if [[ "$OUT" == *"Would write: ${HOME}/Library/LaunchAgents/com.localgithub.lgh.plist"* ]]; then
    fail "dry-run still planned to write our lgh plist"
fi
[[ ! -e "${HOME}/Library/LaunchAgents/com.localgithub.lgh.plist" ]] || fail "dry-run created lgh plist"
assert_ok "$SCRIPT" status
assert_contains "Homebrew: launchd label homebrew.mxcl.lgh"
assert_contains "not loaded"
cleanup
WORK=""

# --- macOS: canonical label loaded with a PID, no plist file ---
make_env
export AUTOSTART_PLATFORM=Darwin
printf '%s\n' '915 0 sh.brew.actiond' '-' '0' 'com.other.thing' >"$LAUNCHCTL_LIST"
assert_ok "$SCRIPT" status
assert_contains "launchd label sh.brew.actiond"
assert_contains "running PID 915"
assert_ok "$SCRIPT" install --dry-run
assert_contains "Skipping ActionD"
assert_not_contains "Skipping LGH"
cleanup
WORK=""

# --- macOS: LaunchDaemon path and uninstall leaves brew plist ---
make_env
export AUTOSTART_PLATFORM=Darwin
mkdir -p "${HOME}/Library/LaunchAgents"
: >"${AUTOSTART_LAUNCH_DAEMON_DIR}/sh.brew.lgh.plist"
printf '%s\n' '<?xml version="1.0"?>' '<plist><dict></dict></plist>' \
    >"${HOME}/Library/LaunchAgents/com.localgithub.lgh.plist"
assert_ok "$SCRIPT" uninstall
[[ ! -e "${HOME}/Library/LaunchAgents/com.localgithub.lgh.plist" ]] || fail "our plist was kept"
[[ -e "${AUTOSTART_LAUNCH_DAEMON_DIR}/sh.brew.lgh.plist" ]] || fail "brew daemon plist was removed"
assert_contains "Left Homebrew-managed LGH"
assert_ok "$SCRIPT" install --dry-run --ignore-homebrew
assert_contains "Would write: ${HOME}/Library/LaunchAgents/com.localgithub.lgh.plist"
cleanup
WORK=""

# --- macOS: both managers registered ---
make_env
export AUTOSTART_PLATFORM=Darwin
mkdir -p "${HOME}/Library/LaunchAgents"
: >"${HOME}/Library/LaunchAgents/homebrew.mxcl.actiond.plist"
: >"${HOME}/Library/LaunchAgents/com.localgithub.actiond.plist"
assert_ok "$SCRIPT" status
assert_contains "Conflict: Homebrew and com.localgithub.actiond are both registered"
cleanup
WORK=""

echo "autostart_test: ok"
