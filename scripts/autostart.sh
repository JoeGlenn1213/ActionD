#!/usr/bin/env bash
# autostart.sh - Install/uninstall login-time service units for ActionD and LGH
# SPDX-License-Identifier: MIT
#
# Usage:
#   scripts/autostart.sh install   [options]
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
REQUIRED_PYTHON_MAJOR=3
REQUIRED_PYTHON_MINOR=8

LGH_PLIST_LABEL="com.localgithub.lgh"
ACTIOND_PLIST_LABEL="com.localgithub.actiond"
LGH_SERVICE="lgh.service"
ACTIOND_SERVICE="actiond.service"

# Options (set via command line)
DRY_RUN=false
FORCE=false
OPT_LGH_BIN=""
OPT_ACTIOND_BIN=""
OPT_LOG_DIR=""
declare -a EXTRA_ENV=()

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
        warn "Python 3 not found. ActionD plugins require Python ${REQUIRED_PYTHON_MAJOR}.${REQUIRED_PYTHON_MINOR}+."
        warn "ActionD core will work, but Python-based plugins will fail."
        return 0
    fi
    py_version="$("$py_path" -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')"
    major="${py_version%%.*}"
    minor="${py_version#*.}"
    if (( major < REQUIRED_PYTHON_MAJOR )) || { (( major == REQUIRED_PYTHON_MAJOR )) && (( minor < REQUIRED_PYTHON_MINOR )); }; then
        warn "Python ${py_version} found but ActionD plugins require ${REQUIRED_PYTHON_MAJOR}.${REQUIRED_PYTHON_MINOR}+."
        warn "ActionD core will work, but some plugins may fail."
        return 0
    fi
    info "Python ${py_version} OK (${py_path})"
}

backup_file() {
    local file="$1"
    local backup="${file}.bak.$(date +%Y%m%d-%H%M%S)"
    if $DRY_RUN; then
        info "[dry-run] Would backup: $file -> $backup"
    else
        cp -p "$file" "$backup"
        info "Backed up: $file -> $backup"
    fi
}

# Extract binary path from existing plist (macOS)
extract_plist_bin() {
    local plist="$1"
    if [[ -f "$plist" ]]; then
        /usr/libexec/PlistBuddy -c "Print :ProgramArguments:0" "$plist" 2>/dev/null || true
    fi
}

# Extract binary path from existing systemd unit (Linux)
extract_unit_bin() {
    local unit="$1"
    if [[ -f "$unit" ]]; then
        grep -E '^ExecStart=' "$unit" 2>/dev/null | sed 's/^ExecStart=//' | awk '{print $1}' || true
    fi
}

# Get PID from launchctl (macOS)
get_launchctl_pid() {
    local label="$1"
    local pid
    # Try launchctl list first (shows PID or '-' if not running)
    pid=$(launchctl list 2>/dev/null | awk -v label="$label" '$3 == label {print $1}')
    if [[ "$pid" == "-" || -z "$pid" ]]; then
        echo ""
    else
        echo "$pid"
    fi
}

# Check if systemd service is actually running (Linux)
is_systemd_active() {
    local service="$1"
    systemctl --user is-active "$service" &>/dev/null
}

#------------------------------------------------------------------------------
# macOS LaunchAgent plist generation
#------------------------------------------------------------------------------
generate_lgh_plist() {
    local bin_path="$1"
    local log_dir="$2"
    local current_path="${PATH:-/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin}"
    
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
    <string>${log_dir}/lgh.log</string>
    <key>StandardErrorPath</key>
    <string>${log_dir}/lgh.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>${current_path}</string>
EOF
    # Add extra environment variables
    for env_pair in "${EXTRA_ENV[@]}"; do
        local key="${env_pair%%=*}"
        local val="${env_pair#*=}"
        cat <<EOF
        <key>${key}</key>
        <string>${val}</string>
EOF
    done
    cat <<EOF
    </dict>
</dict>
</plist>
EOF
}

generate_actiond_plist() {
    local bin_path="$1"
    local log_dir="$2"
    local current_path="${PATH:-/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin}"
    
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
    <string>${log_dir}/actiond.log</string>
    <key>StandardErrorPath</key>
    <string>${log_dir}/actiond.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>${current_path}</string>
EOF
    # Add extra environment variables
    for env_pair in "${EXTRA_ENV[@]}"; do
        local key="${env_pair%%=*}"
        local val="${env_pair#*=}"
        cat <<EOF
        <key>${key}</key>
        <string>${val}</string>
EOF
    done
    cat <<EOF
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
EOF
    # Add extra environment variables
    for env_pair in "${EXTRA_ENV[@]}"; do
        echo "Environment=${env_pair}"
    done
    cat <<EOF

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
EOF
    # Add extra environment variables
    for env_pair in "${EXTRA_ENV[@]}"; do
        echo "Environment=${env_pair}"
    done
    cat <<EOF

[Install]
WantedBy=default.target
EOF
}

#------------------------------------------------------------------------------
# macOS install/uninstall/status
#------------------------------------------------------------------------------
install_macos() {
    local log_dir="${OPT_LOG_DIR:-${HOME}/Library/Logs}"
    
    info "Installing LaunchAgents for macOS..."
    info "Log directory: $log_dir"
    info "Current PATH will be preserved in service definitions"

    local lgh_bin actiond_bin lgh_plist actiond_plist
    lgh_plist="${LAUNCHAGENT_DIR}/${LGH_PLIST_LABEL}.plist"
    actiond_plist="${LAUNCHAGENT_DIR}/${ACTIOND_PLIST_LABEL}.plist"

    # Determine binaries
    if [[ -n "$OPT_LGH_BIN" ]]; then
        lgh_bin="$OPT_LGH_BIN"
    else
        lgh_bin="$(resolve_bin lgh)"
    fi
    
    if [[ -n "$OPT_ACTIOND_BIN" ]]; then
        actiond_bin="$OPT_ACTIOND_BIN"
    else
        actiond_bin="$(resolve_bin actiond)"
    fi

    # Check for existing plists
    local existing_lgh_bin existing_actiond_bin
    existing_lgh_bin="$(extract_plist_bin "$lgh_plist")"
    existing_actiond_bin="$(extract_plist_bin "$actiond_plist")"

    if [[ -f "$lgh_plist" ]] && ! $FORCE; then
        error "LGH plist already exists: $lgh_plist
  Current binary: ${existing_lgh_bin:-unknown}
  Use --force to overwrite (will backup existing file first)
  Or use --lgh-bin to specify a different binary path"
    fi

    if [[ -f "$actiond_plist" ]] && ! $FORCE; then
        error "ActionD plist already exists: $actiond_plist
  Current binary: ${existing_actiond_bin:-unknown}
  Use --force to overwrite (will backup existing file first)
  Or use --actiond-bin to specify a different binary path"
    fi

    # Create directories (only if not dry-run)
    if ! $DRY_RUN; then
        mkdir -p "$LAUNCHAGENT_DIR" "$log_dir"
    fi

    # LGH
    if [[ -n "$lgh_bin" ]]; then
        info "Using lgh binary: $lgh_bin"
        if [[ -f "$lgh_plist" ]]; then
            backup_file "$lgh_plist"
        fi
        if $DRY_RUN; then
            info "[dry-run] Would write: $lgh_plist"
            echo "--- Generated plist content ---"
            generate_lgh_plist "$lgh_bin" "$log_dir"
            echo "--- End plist content ---"
        else
            generate_lgh_plist "$lgh_bin" "$log_dir" > "$lgh_plist"
            launchctl bootout "gui/$(id -u)/${LGH_PLIST_LABEL}" 2>/dev/null || true
            launchctl bootstrap "gui/$(id -u)" "$lgh_plist"
            success "LGH LaunchAgent installed and loaded"
        fi
    else
        warn "lgh not found on PATH; skipping LGH LaunchAgent"
        warn "Use --lgh-bin to specify the binary path"
    fi

    # ActionD
    if [[ -n "$actiond_bin" ]]; then
        info "Using actiond binary: $actiond_bin"
        if [[ -f "$actiond_plist" ]]; then
            backup_file "$actiond_plist"
        fi
        if $DRY_RUN; then
            info "[dry-run] Would write: $actiond_plist"
            echo "--- Generated plist content ---"
            generate_actiond_plist "$actiond_bin" "$log_dir"
            echo "--- End plist content ---"
        else
            generate_actiond_plist "$actiond_bin" "$log_dir" > "$actiond_plist"
            launchctl bootout "gui/$(id -u)/${ACTIOND_PLIST_LABEL}" 2>/dev/null || true
            launchctl bootstrap "gui/$(id -u)" "$actiond_plist"
            success "ActionD LaunchAgent installed and loaded"
        fi
    else
        warn "actiond not found on PATH; skipping ActionD LaunchAgent"
        warn "Use --actiond-bin to specify the binary path"
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
        local lgh_bin pid
        lgh_bin="$(extract_plist_bin "$lgh_plist")"
        echo "  Plist:  $lgh_plist"
        echo "  Binary: ${lgh_bin:-unknown}"
        pid="$(get_launchctl_pid "$LGH_PLIST_LABEL")"
        if [[ -n "$pid" ]]; then
            echo "  Status: running (PID $pid)"
        else
            echo "  Status: loaded but not running"
        fi
    else
        echo "  Status: not installed"
    fi

    echo ""
    echo "ActionD:"
    if [[ -f "$actiond_plist" ]]; then
        local actiond_bin pid
        actiond_bin="$(extract_plist_bin "$actiond_plist")"
        echo "  Plist:  $actiond_plist"
        echo "  Binary: ${actiond_bin:-unknown}"
        pid="$(get_launchctl_pid "$ACTIOND_PLIST_LABEL")"
        if [[ -n "$pid" ]]; then
            echo "  Status: running (PID $pid)"
        else
            echo "  Status: loaded but not running"
        fi
    else
        echo "  Status: not installed"
    fi
    echo ""
}

#------------------------------------------------------------------------------
# Linux install/uninstall/status
#------------------------------------------------------------------------------
install_linux() {
    info "Installing systemd --user units for Linux..."

    local lgh_bin actiond_bin lgh_unit actiond_unit
    lgh_unit="${SYSTEMD_USER_DIR}/${LGH_SERVICE}"
    actiond_unit="${SYSTEMD_USER_DIR}/${ACTIOND_SERVICE}"

    # Determine binaries
    if [[ -n "$OPT_LGH_BIN" ]]; then
        lgh_bin="$OPT_LGH_BIN"
    else
        lgh_bin="$(resolve_bin lgh)"
    fi
    
    if [[ -n "$OPT_ACTIOND_BIN" ]]; then
        actiond_bin="$OPT_ACTIOND_BIN"
    else
        actiond_bin="$(resolve_bin actiond)"
    fi

    # Check for existing units
    local existing_lgh_bin existing_actiond_bin
    existing_lgh_bin="$(extract_unit_bin "$lgh_unit")"
    existing_actiond_bin="$(extract_unit_bin "$actiond_unit")"

    if [[ -f "$lgh_unit" ]] && ! $FORCE; then
        error "LGH unit already exists: $lgh_unit
  Current binary: ${existing_lgh_bin:-unknown}
  Use --force to overwrite (will backup existing file first)
  Or use --lgh-bin to specify a different binary path"
    fi

    if [[ -f "$actiond_unit" ]] && ! $FORCE; then
        error "ActionD unit already exists: $actiond_unit
  Current binary: ${existing_actiond_bin:-unknown}
  Use --force to overwrite (will backup existing file first)
  Or use --actiond-bin to specify a different binary path"
    fi

    # Create directory (only if not dry-run)
    if ! $DRY_RUN; then
        mkdir -p "$SYSTEMD_USER_DIR"
    fi

    # LGH
    if [[ -n "$lgh_bin" ]]; then
        info "Using lgh binary: $lgh_bin"
        if [[ -f "$lgh_unit" ]]; then
            backup_file "$lgh_unit"
        fi
        if $DRY_RUN; then
            info "[dry-run] Would write: $lgh_unit"
            echo "--- Generated unit content ---"
            generate_lgh_unit "$lgh_bin"
            echo "--- End unit content ---"
        else
            generate_lgh_unit "$lgh_bin" > "$lgh_unit"
            systemctl --user daemon-reload
            systemctl --user enable --now "$LGH_SERVICE"
            success "LGH systemd unit installed and enabled"
        fi
    else
        warn "lgh not found on PATH; skipping LGH systemd unit"
        warn "Use --lgh-bin to specify the binary path"
    fi

    # ActionD
    if [[ -n "$actiond_bin" ]]; then
        info "Using actiond binary: $actiond_bin"
        if [[ -f "$actiond_unit" ]]; then
            backup_file "$actiond_unit"
        fi
        if $DRY_RUN; then
            info "[dry-run] Would write: $actiond_unit"
            echo "--- Generated unit content ---"
            generate_actiond_unit "$actiond_bin"
            echo "--- End unit content ---"
        else
            generate_actiond_unit "$actiond_bin" > "$actiond_unit"
            systemctl --user daemon-reload
            systemctl --user enable --now "$ACTIOND_SERVICE"
            success "ActionD systemd unit installed and enabled"
        fi
    else
        warn "actiond not found on PATH; skipping ActionD systemd unit"
        warn "Use --actiond-bin to specify the binary path"
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
        local lgh_bin
        lgh_bin="$(extract_unit_bin "$lgh_unit")"
        echo "  Unit:   $lgh_unit"
        echo "  Binary: ${lgh_bin:-unknown}"
        if is_systemd_active "$LGH_SERVICE"; then
            local pid
            pid="$(systemctl --user show "$LGH_SERVICE" --property=MainPID --value 2>/dev/null || echo "")"
            echo "  Status: running (PID ${pid:-unknown})"
        else
            echo "  Status: installed but not running"
        fi
    else
        echo "  Status: not installed"
    fi

    echo ""
    echo "ActionD:"
    if [[ -f "$actiond_unit" ]]; then
        local actiond_bin
        actiond_bin="$(extract_unit_bin "$actiond_unit")"
        echo "  Unit:   $actiond_unit"
        echo "  Binary: ${actiond_bin:-unknown}"
        if is_systemd_active "$ACTIOND_SERVICE"; then
            local pid
            pid="$(systemctl --user show "$ACTIOND_SERVICE" --property=MainPID --value 2>/dev/null || echo "")"
            echo "  Status: running (PID ${pid:-unknown})"
        else
            echo "  Status: installed but not running"
        fi
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

Install Options:
  --dry-run           Show what would be done without making changes
  --force             Overwrite existing service files (backs up first)
  --lgh-bin PATH      Use specified lgh binary instead of PATH lookup
  --actiond-bin PATH  Use specified actiond binary instead of PATH lookup
  --log-dir DIR       Log directory (macOS only; default: ~/Library/Logs)
  --env KEY=VALUE     Add environment variable (can be repeated)

Uninstall Options:
  --dry-run           Show what would be done without making changes

On macOS: creates user LaunchAgents in ~/Library/LaunchAgents
On Linux: creates systemd --user units in ~/.config/systemd/user

The current PATH is captured at install time and preserved in service definitions.
Existing service files are never overwritten without --force.

Prerequisites:
  - Python 3.8+ recommended for ActionD plugins (warning if missing)
  - lgh and/or actiond on PATH or specified via --lgh-bin/--actiond-bin
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
            --force)
                FORCE=true
                shift
                ;;
            --lgh-bin)
                [[ $# -ge 2 ]] || error "--lgh-bin requires a path argument"
                OPT_LGH_BIN="$2"
                shift 2
                ;;
            --actiond-bin)
                [[ $# -ge 2 ]] || error "--actiond-bin requires a path argument"
                OPT_ACTIOND_BIN="$2"
                shift 2
                ;;
            --log-dir)
                [[ $# -ge 2 ]] || error "--log-dir requires a path argument"
                OPT_LOG_DIR="$2"
                shift 2
                ;;
            --env)
                [[ $# -ge 2 ]] || error "--env requires a KEY=VALUE argument"
                [[ "$2" == *"="* ]] || error "--env argument must be KEY=VALUE format"
                EXTRA_ENV+=("$2")
                shift 2
                ;;
            -*)
                error "Unknown option: $1"
                ;;
            *)
                error "Unexpected argument: $1"
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
