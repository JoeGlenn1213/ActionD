#!/usr/bin/env bash
# autostart.sh - Install/uninstall login-time service units for ActionD and LGH
# SPDX-License-Identifier: MIT
#
# Usage:
#   scripts/autostart.sh install   [--dry-run]
#   scripts/autostart.sh uninstall [--dry-run]
#   scripts/autostart.sh status
#
# On macOS: creates LaunchAgents in ~/Library/LaunchAgents (no sudo required)
# On Linux: creates systemd --user units in ~/.config/systemd/user

set -euo pipefail

#------------------------------------------------------------------------------
# Configuration
#------------------------------------------------------------------------------
LAUNCHAGENT_DIR="${HOME}/Library/LaunchAgents"
SYSTEMD_USER_DIR="${HOME}/.config/systemd/user"
LOG_DIR_MACOS="${HOME}/Library/Logs"
REQUIRED_PYTHON_MAJOR=3
REQUIRED_PYTHON_MINOR=8

LGH_PLIST_LABEL="com.localgithub.lgh"
ACTIOND_PLIST_LABEL="com.localgithub.actiond"
LGH_SERVICE="lgh.service"
ACTIOND_SERVICE="actiond.service"

DRY_RUN=false

#------------------------------------------------------------------------------
# Helpers
#------------------------------------------------------------------------------
info()  { printf '\033[1;34m[INFO]\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m[WARN]\033[0m %s\n' "$*" >&2; }
error() { printf '\033[1;31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }
success() { printf '\033[1;32m[OK]\033[0m %s\n' "$*"; }

is_macos() { [[ "$(uname -s)" == "Darwin" ]]; }
is_linux() { [[ "$(uname -s)" == "Linux" ]]; }

resolve_bin() {
    local name="$1"
    local path
    path="$(command -v "$name" 2>/dev/null || true)"
    if [[ -n "$path" ]]; then
        # Follow symlinks to get absolute path
        if is_macos; then
            path="$(cd "$(dirname "$path")" && pwd -P)/$(basename "$path")"
        else
            path="$(readlink -f "$path")"
        fi
    fi
    echo "$path"
}

check_python() {
    local py_path py_version major minor
    py_path="$(resolve_bin python3)"
    if [[ -z "$py_path" ]]; then
        error "Python 3 not found. ActionD plugins require Python ${REQUIRED_PYTHON_MAJOR}.${REQUIRED_PYTHON_MINOR}+."
    fi
    py_version="$("$py_path" -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')"
    major="${py_version%%.*}"
    minor="${py_version#*.}"
    if (( major < REQUIRED_PYTHON_MAJOR )) || { (( major == REQUIRED_PYTHON_MAJOR )) && (( minor < REQUIRED_PYTHON_MINOR )); }; then
        error "Python ${py_version} found but ActionD requires ${REQUIRED_PYTHON_MAJOR}.${REQUIRED_PYTHON_MINOR}+."
    fi
    info "Python ${py_version} OK (${py_path})"
}

#------------------------------------------------------------------------------
# macOS LaunchAgent plist generation
#------------------------------------------------------------------------------
generate_lgh_plist() {
    local bin_path="$1"
    cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${LGH_PLIST_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${bin_path}</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${LOG_DIR_MACOS}/lgh.log</string>
    <key>StandardErrorPath</key>
    <string>${LOG_DIR_MACOS}/lgh.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
    </dict>
</dict>
</plist>
EOF
}

generate_actiond_plist() {
    local bin_path="$1"
    cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${ACTIOND_PLIST_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${bin_path}</string>
        <string>start</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>${LOG_DIR_MACOS}/actiond.log</string>
    <key>StandardErrorPath</key>
    <string>${LOG_DIR_MACOS}/actiond.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
    </dict>
</dict>
</plist>
EOF
}

#------------------------------------------------------------------------------
# Linux systemd user unit generation
#------------------------------------------------------------------------------
generate_lgh_unit() {
    local bin_path="$1"
    cat <<EOF
[Unit]
Description=LGH - Local Git Hub server
After=network.target

[Service]
Type=simple
ExecStart=${bin_path} serve
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
}

generate_actiond_unit() {
    local bin_path="$1"
    cat <<EOF
[Unit]
Description=ActionD - Local AI Action Execution Engine
After=network.target lgh.service
Wants=lgh.service

[Service]
Type=simple
ExecStart=${bin_path} start
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
}

#------------------------------------------------------------------------------
# macOS install/uninstall
#------------------------------------------------------------------------------
install_macos() {
    info "Installing LaunchAgents for macOS..."
    mkdir -p "$LAUNCHAGENT_DIR" "$LOG_DIR_MACOS"

    local lgh_bin actiond_bin lgh_plist actiond_plist
    lgh_bin="$(resolve_bin lgh)"
    actiond_bin="$(resolve_bin actiond)"

    lgh_plist="${LAUNCHAGENT_DIR}/${LGH_PLIST_LABEL}.plist"
    actiond_plist="${LAUNCHAGENT_DIR}/${ACTIOND_PLIST_LABEL}.plist"

    # LGH
    if [[ -n "$lgh_bin" ]]; then
        info "Found lgh at: $lgh_bin"
        if $DRY_RUN; then
            info "[dry-run] Would write: $lgh_plist"
            generate_lgh_plist "$lgh_bin"
        else
            generate_lgh_plist "$lgh_bin" > "$lgh_plist"
            launchctl bootout "gui/$(id -u)/${LGH_PLIST_LABEL}" 2>/dev/null || true
            launchctl bootstrap "gui/$(id -u)" "$lgh_plist"
            success "LGH LaunchAgent installed and loaded"
        fi
    else
        warn "lgh not found on PATH; skipping LGH LaunchAgent"
    fi

    # ActionD
    if [[ -n "$actiond_bin" ]]; then
        info "Found actiond at: $actiond_bin"
        if $DRY_RUN; then
            info "[dry-run] Would write: $actiond_plist"
            generate_actiond_plist "$actiond_bin"
        else
            generate_actiond_plist "$actiond_bin" > "$actiond_plist"
            launchctl bootout "gui/$(id -u)/${ACTIOND_PLIST_LABEL}" 2>/dev/null || true
            launchctl bootstrap "gui/$(id -u)" "$actiond_plist"
            success "ActionD LaunchAgent installed and loaded"
        fi
    else
        warn "actiond not found on PATH; skipping ActionD LaunchAgent"
    fi
}

uninstall_macos() {
    info "Uninstalling LaunchAgents for macOS..."
    local lgh_plist="${LAUNCHAGENT_DIR}/${LGH_PLIST_LABEL}.plist"
    local actiond_plist="${LAUNCHAGENT_DIR}/${ACTIOND_PLIST_LABEL}.plist"

    # ActionD first (depends on LGH)
    if [[ -f "$actiond_plist" ]]; then
        if $DRY_RUN; then
            info "[dry-run] Would unload and remove: $actiond_plist"
        else
            launchctl bootout "gui/$(id -u)/${ACTIOND_PLIST_LABEL}" 2>/dev/null || true
            rm -f "$actiond_plist"
            success "ActionD LaunchAgent uninstalled"
        fi
    else
        info "ActionD LaunchAgent not found"
    fi

    # LGH
    if [[ -f "$lgh_plist" ]]; then
        if $DRY_RUN; then
            info "[dry-run] Would unload and remove: $lgh_plist"
        else
            launchctl bootout "gui/$(id -u)/${LGH_PLIST_LABEL}" 2>/dev/null || true
            rm -f "$lgh_plist"
            success "LGH LaunchAgent uninstalled"
        fi
    else
        info "LGH LaunchAgent not found"
    fi
}

status_macos() {
    info "Checking LaunchAgent status..."
    local lgh_plist="${LAUNCHAGENT_DIR}/${LGH_PLIST_LABEL}.plist"
    local actiond_plist="${LAUNCHAGENT_DIR}/${ACTIOND_PLIST_LABEL}.plist"

    echo ""
    echo "LGH:"
    if [[ -f "$lgh_plist" ]]; then
        echo "  Plist: $lgh_plist (installed)"
        if launchctl print "gui/$(id -u)/${LGH_PLIST_LABEL}" &>/dev/null; then
            echo "  Status: loaded (running)"
        else
            echo "  Status: installed but not loaded"
        fi
    else
        echo "  Status: not installed"
    fi

    echo ""
    echo "ActionD:"
    if [[ -f "$actiond_plist" ]]; then
        echo "  Plist: $actiond_plist (installed)"
        if launchctl print "gui/$(id -u)/${ACTIOND_PLIST_LABEL}" &>/dev/null; then
            echo "  Status: loaded (running)"
        else
            echo "  Status: installed but not loaded"
        fi
    else
        echo "  Status: not installed"
    fi
    echo ""
}

#------------------------------------------------------------------------------
# Linux install/uninstall
#------------------------------------------------------------------------------
install_linux() {
    info "Installing systemd --user units for Linux..."
    mkdir -p "$SYSTEMD_USER_DIR"

    local lgh_bin actiond_bin lgh_unit actiond_unit
    lgh_bin="$(resolve_bin lgh)"
    actiond_bin="$(resolve_bin actiond)"

    lgh_unit="${SYSTEMD_USER_DIR}/${LGH_SERVICE}"
    actiond_unit="${SYSTEMD_USER_DIR}/${ACTIOND_SERVICE}"

    # LGH
    if [[ -n "$lgh_bin" ]]; then
        info "Found lgh at: $lgh_bin"
        if $DRY_RUN; then
            info "[dry-run] Would write: $lgh_unit"
            generate_lgh_unit "$lgh_bin"
        else
            generate_lgh_unit "$lgh_bin" > "$lgh_unit"
            systemctl --user daemon-reload
            systemctl --user enable --now "$LGH_SERVICE"
            success "LGH systemd unit installed and enabled"
        fi
    else
        warn "lgh not found on PATH; skipping LGH systemd unit"
    fi

    # ActionD
    if [[ -n "$actiond_bin" ]]; then
        info "Found actiond at: $actiond_bin"
        if $DRY_RUN; then
            info "[dry-run] Would write: $actiond_unit"
            generate_actiond_unit "$actiond_bin"
        else
            generate_actiond_unit "$actiond_bin" > "$actiond_unit"
            systemctl --user daemon-reload
            systemctl --user enable --now "$ACTIOND_SERVICE"
            success "ActionD systemd unit installed and enabled"
        fi
    else
        warn "actiond not found on PATH; skipping ActionD systemd unit"
    fi

    # Note about linger for start-at-boot without login
    if [[ -n "$lgh_bin" || -n "$actiond_bin" ]] && ! $DRY_RUN; then
        echo ""
        warn "Note: For services to start at boot without logging in, run:"
        echo "      loginctl enable-linger \$USER"
        echo ""
    fi
}

uninstall_linux() {
    info "Uninstalling systemd --user units for Linux..."
    local lgh_unit="${SYSTEMD_USER_DIR}/${LGH_SERVICE}"
    local actiond_unit="${SYSTEMD_USER_DIR}/${ACTIOND_SERVICE}"

    # ActionD first (depends on LGH)
    if [[ -f "$actiond_unit" ]]; then
        if $DRY_RUN; then
            info "[dry-run] Would disable and remove: $actiond_unit"
        else
            systemctl --user disable --now "$ACTIOND_SERVICE" 2>/dev/null || true
            rm -f "$actiond_unit"
            success "ActionD systemd unit uninstalled"
        fi
    else
        info "ActionD systemd unit not found"
    fi

    # LGH
    if [[ -f "$lgh_unit" ]]; then
        if $DRY_RUN; then
            info "[dry-run] Would disable and remove: $lgh_unit"
        else
            systemctl --user disable --now "$LGH_SERVICE" 2>/dev/null || true
            rm -f "$lgh_unit"
            success "LGH systemd unit uninstalled"
        fi
    else
        info "LGH systemd unit not found"
    fi

    if ! $DRY_RUN; then
        systemctl --user daemon-reload 2>/dev/null || true
    fi
}

status_linux() {
    info "Checking systemd --user unit status..."
    local lgh_unit="${SYSTEMD_USER_DIR}/${LGH_SERVICE}"
    local actiond_unit="${SYSTEMD_USER_DIR}/${ACTIOND_SERVICE}"

    echo ""
    echo "LGH:"
    if [[ -f "$lgh_unit" ]]; then
        echo "  Unit: $lgh_unit (installed)"
        systemctl --user status "$LGH_SERVICE" --no-pager 2>/dev/null || echo "  Status: not running"
    else
        echo "  Status: not installed"
    fi

    echo ""
    echo "ActionD:"
    if [[ -f "$actiond_unit" ]]; then
        echo "  Unit: $actiond_unit (installed)"
        systemctl --user status "$ACTIOND_SERVICE" --no-pager 2>/dev/null || echo "  Status: not running"
    else
        echo "  Status: not installed"
    fi

    # Linger status
    echo ""
    if loginctl show-user "$USER" 2>/dev/null | grep -q "Linger=yes"; then
        echo "Linger: enabled (services will start at boot without login)"
    else
        echo "Linger: disabled"
        echo "  Run 'loginctl enable-linger \$USER' to start services at boot without login"
    fi
    echo ""
}

#------------------------------------------------------------------------------
# Main
#------------------------------------------------------------------------------
usage() {
    cat <<EOF
Usage: $(basename "$0") <command> [options]

Commands:
  install     Install and enable start-on-boot services
  uninstall   Disable and remove start-on-boot services
  status      Show current service status

Options:
  --dry-run   Show what would be done without making changes

On macOS: creates user LaunchAgents in ~/Library/LaunchAgents
On Linux: creates systemd --user units in ~/.config/systemd/user

Prerequisites:
  - Python 3.8+ (required for ActionD plugins)
  - lgh and/or actiond on PATH (skipped gracefully if not found)
EOF
    exit 1
}

main() {
    local cmd="${1:-}"
    shift || true

    # Parse options
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --dry-run)
                DRY_RUN=true
                shift
                ;;
            *)
                error "Unknown option: $1"
                ;;
        esac
    done

    case "$cmd" in
        install)
            check_python
            if is_macos; then
                install_macos
            elif is_linux; then
                install_linux
            else
                error "Unsupported platform: $(uname -s)"
            fi
            ;;
        uninstall)
            if is_macos; then
                uninstall_macos
            elif is_linux; then
                uninstall_linux
            else
                error "Unsupported platform: $(uname -s)"
            fi
            ;;
        status)
            if is_macos; then
                status_macos
            elif is_linux; then
                status_linux
            else
                error "Unsupported platform: $(uname -s)"
            fi
            ;;
        -h|--help|help|"")
            usage
            ;;
        *)
            error "Unknown command: $cmd"
            ;;
    esac
}

main "$@"
