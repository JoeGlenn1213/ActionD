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
#
# If brew services already registered the formula, install skips that service.
# Homebrew's default name is sh.brew.<formula> (service.rb canonical_*).
# It still resolves the legacy names homebrew.mxcl.<formula> (macOS launchd)
# and homebrew.<formula> (Linux systemd). Pass --ignore-homebrew to install
# our agent anyway.

set -euo pipefail

#------------------------------------------------------------------------------
# Configuration
#------------------------------------------------------------------------------
LAUNCHAGENT_DIR="${HOME}/Library/LaunchAgents"
# Root brew services uses /Library/LaunchDaemons. Tests may point this elsewhere.
LAUNCH_DAEMON_DIR="${AUTOSTART_LAUNCH_DAEMON_DIR:-/Library/LaunchDaemons}"
SYSTEMD_USER_DIR="${HOME}/.config/systemd/user"
# Root brew services uses /usr/lib/systemd/system. Tests may point this elsewhere.
SYSTEMD_SYSTEM_DIR="${AUTOSTART_SYSTEMD_SYSTEM_DIR:-/usr/lib/systemd/system}"
REQUIRED_PYTHON_MAJOR=3
REQUIRED_PYTHON_MINOR=8

LGH_PLIST_LABEL="com.localgithub.lgh"
ACTIOND_PLIST_LABEL="com.localgithub.actiond"
LGH_SERVICE="lgh.service"
ACTIOND_SERVICE="actiond.service"

# Options (set via command line)
DRY_RUN=false
FORCE=false
IGNORE_HOMEBREW=false
OPT_LGH_BIN=""
OPT_ACTIOND_BIN=""
OPT_PATH=""
OPT_LGH_LOG_DIR=""
OPT_ACTIOND_LOG_DIR=""
declare -a OPT_LGH_ENV=()
declare -a OPT_ACTIOND_ENV=()

#------------------------------------------------------------------------------
# Helpers
#------------------------------------------------------------------------------
info()  { printf '\033[1;34m[INFO]\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m[WARN]\033[0m %s\n' "$*" >&2; }
error() { printf '\033[1;31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }
success() { printf '\033[1;32m[OK]\033[0m %s\n' "$*"; }

# AUTOSTART_PLATFORM overrides uname for tests (Darwin or Linux). Leave it unset.
autostart_uname() {
    if [[ -n "${AUTOSTART_PLATFORM:-}" ]]; then
        printf '%s\n' "$AUTOSTART_PLATFORM"
    else
        uname -s
    fi
}

is_macos() { [[ "$(autostart_uname)" == "Darwin" ]]; }
is_linux() { [[ "$(autostart_uname)" == "Linux" ]]; }

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

# Build a minimal sane PATH for a binary
build_minimal_path() {
    local bin_path="$1"
    local parts=()
    local seen=()
    
    add_if_new() {
        local p="$1"
        [[ -d "$p" ]] || return 0
        for s in "${seen[@]+"${seen[@]}"}"; do
            [[ "$s" == "$p" ]] && return 0
        done
        seen+=("$p")
        parts+=("$p")
    }
    
    # 1. Directory of the chosen binary
    if [[ -n "$bin_path" ]]; then
        add_if_new "$(dirname "$bin_path")"
    fi
    
    # 2. Homebrew prefix bin (detect from brew or common locations)
    if command -v brew &>/dev/null; then
        add_if_new "$(brew --prefix 2>/dev/null)/bin"
    elif [[ -d /opt/homebrew/bin ]]; then
        add_if_new /opt/homebrew/bin
    elif [[ -d /usr/local/bin ]]; then
        add_if_new /usr/local/bin
    elif [[ -d /home/linuxbrew/.linuxbrew/bin ]]; then
        add_if_new /home/linuxbrew/.linuxbrew/bin
    fi
    
    # 3. Standard system paths
    add_if_new /usr/bin
    add_if_new /bin
    add_if_new /usr/sbin
    add_if_new /sbin
    
    # Join with colons
    local IFS=':'
    echo "${parts[*]}"
}

# Extract PATH from existing plist
extract_plist_path() {
    local plist="$1"
    if [[ -f "$plist" ]]; then
        /usr/libexec/PlistBuddy -c "Print :EnvironmentVariables:PATH" "$plist" 2>/dev/null || true
    fi
}

# Extract PATH from existing systemd unit
extract_unit_path() {
    local unit="$1"
    if [[ -f "$unit" ]]; then
        grep -E '^Environment=PATH=' "$unit" 2>/dev/null | sed 's/^Environment=PATH=//' || true
    fi
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

# XML-escape a string for plist values
xml_escape() {
    local s="$1"
    s="${s//&/&amp;}"
    s="${s//</&lt;}"
    s="${s//>/&gt;}"
    s="${s//\"/&quot;}"
    s="${s//\'/&apos;}"
    echo "$s"
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

# Check if launchd job is loaded (macOS)
is_launchd_loaded() {
    local label="$1"
    launchctl list 2>/dev/null | awk -v label="$label" '$3 == label {found=1} END {exit !found}'
}

# Get PID from launchctl (macOS)
get_launchctl_pid() {
    local label="$1"
    local pid
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

# Homebrew brew services names, from Homebrew Library/Homebrew/service.rb:
#   default/canonical plist + systemd name: sh.brew.<formula>
#   legacy macOS launchd label:             homebrew.mxcl.<formula>
#   legacy Linux systemd unit:              homebrew.<formula>
# brew services copies that file into the user (or root) service directory
# and loads it. Either the file or a loaded label means it is registered.
# JoeGlenn1213/tap formulae do not set an explicit service name, so both apply.
describe_homebrew_service() {
    local formula="$1"
    if is_macos; then
        describe_homebrew_launchd "$formula"
    elif is_linux; then
        describe_homebrew_systemd "$formula"
    else
        return 1
    fi
}

describe_homebrew_launchd() {
    local formula="$1"
    local -a labels=("sh.brew.${formula}" "homebrew.mxcl.${formula}")
    local label user_plist boot_plist pid where

    for label in "${labels[@]}"; do
        if is_launchd_loaded "$label"; then
            pid="$(get_launchctl_pid "$label")"
            user_plist="${LAUNCHAGENT_DIR}/${label}.plist"
            boot_plist="${LAUNCH_DAEMON_DIR}/${label}.plist"
            where="loaded in launchd"
            if [[ -f "$user_plist" ]]; then
                where="plist ${user_plist}"
            elif [[ -f "$boot_plist" ]]; then
                where="plist ${boot_plist}"
            fi
            if [[ -n "$pid" ]]; then
                printf 'launchd label %s (%s, running PID %s)\n' "$label" "$where" "$pid"
            else
                printf 'launchd label %s (%s, loaded but not running)\n' "$label" "$where"
            fi
            return 0
        fi
    done

    for label in "${labels[@]}"; do
        user_plist="${LAUNCHAGENT_DIR}/${label}.plist"
        if [[ -f "$user_plist" ]]; then
            printf 'launchd label %s (plist %s, not loaded)\n' "$label" "$user_plist"
            return 0
        fi
    done

    for label in "${labels[@]}"; do
        boot_plist="${LAUNCH_DAEMON_DIR}/${label}.plist"
        if [[ -f "$boot_plist" ]]; then
            printf 'launchd label %s (plist %s, not loaded)\n' "$label" "$boot_plist"
            return 0
        fi
    done
    return 1
}

systemd_user_enabled() {
    local unit="$1"
    command -v systemctl >/dev/null 2>&1 || return 1
    systemctl --user is-enabled "$unit" &>/dev/null
}

systemd_user_pid() {
    local unit="$1"
    local pid=""
    command -v systemctl >/dev/null 2>&1 || return 0
    pid="$(systemctl --user show "$unit" --property=MainPID --value 2>/dev/null || true)"
    if [[ -z "$pid" || "$pid" == "0" ]]; then
        return 0
    fi
    printf '%s' "$pid"
}

describe_homebrew_systemd() {
    local formula="$1"
    local -a names=("sh.brew.${formula}" "homebrew.${formula}")
    local name unit user_unit boot_unit pid

    for name in "${names[@]}"; do
        unit="${name}.service"
        if is_systemd_active "$unit"; then
            pid="$(systemd_user_pid "$unit")"
            user_unit="${SYSTEMD_USER_DIR}/${unit}"
            if [[ -n "$pid" ]]; then
                if [[ -f "$user_unit" ]]; then
                    printf 'systemd user unit %s (unit %s, running PID %s)\n' "$name" "$user_unit" "$pid"
                else
                    printf 'systemd user unit %s (running PID %s)\n' "$name" "$pid"
                fi
            else
                printf 'systemd user unit %s (active)\n' "$name"
            fi
            return 0
        fi
    done

    for name in "${names[@]}"; do
        unit="${name}.service"
        if systemd_user_enabled "$unit"; then
            user_unit="${SYSTEMD_USER_DIR}/${unit}"
            if [[ -f "$user_unit" ]]; then
                printf 'systemd user unit %s (unit %s, enabled, not running)\n' "$name" "$user_unit"
            else
                printf 'systemd user unit %s (enabled, not running)\n' "$name"
            fi
            return 0
        fi
    done

    for name in "${names[@]}"; do
        unit="${name}.service"
        user_unit="${SYSTEMD_USER_DIR}/${unit}"
        if [[ -f "$user_unit" ]]; then
            printf 'systemd user unit %s (unit %s, not running)\n' "$name" "$user_unit"
            return 0
        fi
    done

    for name in "${names[@]}"; do
        unit="${name}.service"
        boot_unit="${SYSTEMD_SYSTEM_DIR}/${unit}"
        if [[ -f "$boot_unit" ]]; then
            printf 'systemd system unit %s (unit %s)\n' "$name" "$boot_unit"
            return 0
        fi
    done
    return 1
}

# Return 0 when install must skip this formula because Homebrew registered it.
# own_file, when set, is our agent path; mentioned if it is already there too.
homebrew_blocks_install() {
    local formula="$1"
    local display="$2"
    local own_file="${3:-}"
    local desc=""
    if ! desc="$(describe_homebrew_service "$formula")"; then
        return 1
    fi
    if $IGNORE_HOMEBREW; then
        warn "${display} is already managed by Homebrew (${desc})."
        warn "--ignore-homebrew is set, so our agent will be installed too. Two managers can start two copies."
        return 1
    fi
    warn "Skipping ${display}: Homebrew already manages it (${desc})."
    warn 'Not creating a second agent. Use `brew services` to manage it, or pass --ignore-homebrew to install ours anyway.'
    if [[ -n "$own_file" && -f "$own_file" ]]; then
        warn "Our agent file is also present (${own_file}). Leaving both unchanged."
    fi
    return 0
}

note_homebrew_left() {
    local formula="$1"
    local display="$2"
    local desc=""
    if desc="$(describe_homebrew_service "$formula")"; then
        info "Left Homebrew-managed ${display} in place (${desc})"
    fi
}

#------------------------------------------------------------------------------
# macOS LaunchAgent plist generation
#------------------------------------------------------------------------------
generate_lgh_plist() {
    local bin_path="$1"
    local svc_path="$2"
    local log_dir="$3"
    shift 3
    local -a env_vars=("$@")
    
    local escaped_bin escaped_path escaped_log_dir
    escaped_bin="$(xml_escape "$bin_path")"
    escaped_path="$(xml_escape "$svc_path")"
    escaped_log_dir="$(xml_escape "$log_dir")"
    
    cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${LGH_PLIST_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${escaped_bin}</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>${escaped_log_dir}/lgh.launchd.out</string>
    <key>StandardErrorPath</key>
    <string>${escaped_log_dir}/lgh.launchd.err</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>${escaped_path}</string>
EOF
    for env_pair in "${env_vars[@]+"${env_vars[@]}"}"; do
        local key="${env_pair%%=*}"
        local val="${env_pair#*=}"
        cat <<EOF
        <key>$(xml_escape "$key")</key>
        <string>$(xml_escape "$val")</string>
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
    local svc_path="$2"
    local log_dir="$3"
    shift 3
    local -a env_vars=("$@")
    
    local escaped_bin escaped_path escaped_log_dir
    escaped_bin="$(xml_escape "$bin_path")"
    escaped_path="$(xml_escape "$svc_path")"
    escaped_log_dir="$(xml_escape "$log_dir")"
    
    cat <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${ACTIOND_PLIST_LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${escaped_bin}</string>
        <string>start</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>${escaped_log_dir}/actiond.launchd.out</string>
    <key>StandardErrorPath</key>
    <string>${escaped_log_dir}/actiond.launchd.err</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>${escaped_path}</string>
EOF
    for env_pair in "${env_vars[@]+"${env_vars[@]}"}"; do
        local key="${env_pair%%=*}"
        local val="${env_pair#*=}"
        cat <<EOF
        <key>$(xml_escape "$key")</key>
        <string>$(xml_escape "$val")</string>
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
    local svc_path="$2"
    shift 2
    local -a env_vars=("$@")
    
    cat <<EOF
[Unit]
Description=LGH - Local Git Hub server
After=network.target

[Service]
Type=simple
ExecStart=${bin_path} serve
Restart=on-failure
RestartSec=5
Environment=PATH=${svc_path}
EOF
    for env_pair in "${env_vars[@]+"${env_vars[@]}"}"; do
        echo "Environment=${env_pair}"
    done
    cat <<EOF

[Install]
WantedBy=default.target
EOF
}

generate_actiond_unit() {
    local bin_path="$1"
    local svc_path="$2"
    shift 2
    local -a env_vars=("$@")
    
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
Environment=PATH=${svc_path}
EOF
    for env_pair in "${env_vars[@]+"${env_vars[@]}"}"; do
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
    info "Installing LaunchAgents for macOS..."

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

    # Homebrew-managed services are skipped unless --ignore-homebrew.
    # A skipped service is not an "our file" conflict: --force would only
    # add a second agent beside the one brew services already registered.
    local skip_lgh=false
    local skip_actiond=false
    if homebrew_blocks_install "lgh" "LGH" "$lgh_plist"; then
        skip_lgh=true
    fi
    if homebrew_blocks_install "actiond" "ActionD" "$actiond_plist"; then
        skip_actiond=true
    fi

    # Check for existing plists and collect all conflicts
    local conflicts=()
    local existing_lgh_bin existing_actiond_bin
    existing_lgh_bin="$(extract_plist_bin "$lgh_plist")"
    existing_actiond_bin="$(extract_plist_bin "$actiond_plist")"

    if [[ -f "$lgh_plist" ]] && ! $FORCE && ! $skip_lgh; then
        conflicts+=("LGH plist exists: $lgh_plist (binary: ${existing_lgh_bin:-unknown})")
    fi
    if [[ -f "$actiond_plist" ]] && ! $FORCE && ! $skip_actiond; then
        conflicts+=("ActionD plist exists: $actiond_plist (binary: ${existing_actiond_bin:-unknown})")
    fi

    if [[ ${#conflicts[@]} -gt 0 ]]; then
        {
            echo ""
            printf '\033[1;31m[ERROR]\033[0m Existing service files found:\n'
            for c in "${conflicts[@]}"; do
                echo "  - $c"
            done
            echo ""
            echo "Use --force to overwrite (creates timestamped backup first)."
        } >&2
        exit 1
    fi

    # Determine PATH for each service
    local lgh_path actiond_path
    if [[ -n "$OPT_PATH" ]]; then
        lgh_path="$OPT_PATH"
        actiond_path="$OPT_PATH"
    elif $FORCE; then
        # With --force, preserve existing PATH if present, else use minimal
        local existing_lgh_path existing_actiond_path
        existing_lgh_path="$(extract_plist_path "$lgh_plist")"
        existing_actiond_path="$(extract_plist_path "$actiond_plist")"
        lgh_path="${existing_lgh_path:-$(build_minimal_path "$lgh_bin")}"
        actiond_path="${existing_actiond_path:-$(build_minimal_path "$actiond_bin")}"
    else
        lgh_path="$(build_minimal_path "$lgh_bin")"
        actiond_path="$(build_minimal_path "$actiond_bin")"
    fi

    # Determine log directories
    local lgh_log_dir="${OPT_LGH_LOG_DIR:-${HOME}/Library/Logs}"
    local actiond_log_dir="${OPT_ACTIOND_LOG_DIR:-${HOME}/Library/Logs}"

    info "LGH log directory: $lgh_log_dir"
    info "ActionD log directory: $actiond_log_dir"
    info "LGH PATH: $lgh_path"
    info "ActionD PATH: $actiond_path"

    # Create directories (only if not dry-run)
    if ! $DRY_RUN; then
        mkdir -p "$LAUNCHAGENT_DIR" "$lgh_log_dir" "$actiond_log_dir"
    fi

    # LGH
    if $skip_lgh; then
        :
    elif [[ -n "$lgh_bin" ]]; then
        info "Using lgh binary: $lgh_bin"
        if [[ -f "$lgh_plist" ]]; then
            backup_file "$lgh_plist"
        fi
        if $DRY_RUN; then
            info "[dry-run] Would write: $lgh_plist"
            echo "--- Generated plist content ---"
            generate_lgh_plist "$lgh_bin" "$lgh_path" "$lgh_log_dir" "${OPT_LGH_ENV[@]+"${OPT_LGH_ENV[@]}"}"
            echo "--- End plist content ---"
        else
            generate_lgh_plist "$lgh_bin" "$lgh_path" "$lgh_log_dir" "${OPT_LGH_ENV[@]+"${OPT_LGH_ENV[@]}"}" > "$lgh_plist"
            launchctl bootout "gui/$(id -u)/${LGH_PLIST_LABEL}" 2>/dev/null || true
            launchctl bootstrap "gui/$(id -u)" "$lgh_plist"
            success "LGH LaunchAgent installed and loaded"
        fi
    else
        warn "lgh not found on PATH; skipping LGH LaunchAgent"
        warn "Use --lgh-bin to specify the binary path"
    fi

    # ActionD
    if $skip_actiond; then
        :
    elif [[ -n "$actiond_bin" ]]; then
        info "Using actiond binary: $actiond_bin"
        if [[ -f "$actiond_plist" ]]; then
            backup_file "$actiond_plist"
        fi
        if $DRY_RUN; then
            info "[dry-run] Would write: $actiond_plist"
            echo "--- Generated plist content ---"
            generate_actiond_plist "$actiond_bin" "$actiond_path" "$actiond_log_dir" "${OPT_ACTIOND_ENV[@]+"${OPT_ACTIOND_ENV[@]}"}"
            echo "--- End plist content ---"
        else
            generate_actiond_plist "$actiond_bin" "$actiond_path" "$actiond_log_dir" "${OPT_ACTIOND_ENV[@]+"${OPT_ACTIOND_ENV[@]}"}" > "$actiond_plist"
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

    note_homebrew_left "lgh" "LGH"
    note_homebrew_left "actiond" "ActionD"
}

status_macos() {
    info "Checking LaunchAgent status..."
    local lgh_plist="${LAUNCHAGENT_DIR}/${LGH_PLIST_LABEL}.plist"
    local actiond_plist="${LAUNCHAGENT_DIR}/${ACTIOND_PLIST_LABEL}.plist"
    local lgh_brew="" actiond_brew="" lgh_own=false actiond_own=false

    lgh_brew="$(describe_homebrew_service lgh || true)"
    actiond_brew="$(describe_homebrew_service actiond || true)"

    echo ""
    echo "LGH:"
    if [[ -n "$lgh_brew" ]]; then
        echo "  Homebrew: ${lgh_brew}"
    else
        echo "  Homebrew: not registered"
    fi
    if is_launchd_loaded "$LGH_PLIST_LABEL"; then
        lgh_own=true
        local lgh_bin pid
        lgh_bin="$(extract_plist_bin "$lgh_plist")"
        echo "  Plist:  ${lgh_plist}${lgh_plist:+ (file exists: }$( [[ -f "$lgh_plist" ]] && echo "yes" || echo "no" ))"
        echo "  Binary: ${lgh_bin:-unknown}"
        pid="$(get_launchctl_pid "$LGH_PLIST_LABEL")"
        if [[ -n "$pid" ]]; then
            echo "  Status: running (PID $pid)"
        else
            echo "  Status: loaded but not running"
        fi
    elif [[ -f "$lgh_plist" ]]; then
        lgh_own=true
        local lgh_bin
        lgh_bin="$(extract_plist_bin "$lgh_plist")"
        echo "  Plist:  $lgh_plist"
        echo "  Binary: ${lgh_bin:-unknown}"
        echo "  Status: plist exists but not loaded"
    else
        echo "  Status: not installed"
    fi
    if [[ -n "$lgh_brew" && "$lgh_own" == true ]]; then
        echo "  Conflict: Homebrew and ${LGH_PLIST_LABEL} are both registered"
    fi

    echo ""
    echo "ActionD:"
    if [[ -n "$actiond_brew" ]]; then
        echo "  Homebrew: ${actiond_brew}"
    else
        echo "  Homebrew: not registered"
    fi
    if is_launchd_loaded "$ACTIOND_PLIST_LABEL"; then
        actiond_own=true
        local actiond_bin pid
        actiond_bin="$(extract_plist_bin "$actiond_plist")"
        echo "  Plist:  ${actiond_plist}${actiond_plist:+ (file exists: }$( [[ -f "$actiond_plist" ]] && echo "yes" || echo "no" ))"
        echo "  Binary: ${actiond_bin:-unknown}"
        pid="$(get_launchctl_pid "$ACTIOND_PLIST_LABEL")"
        if [[ -n "$pid" ]]; then
            echo "  Status: running (PID $pid)"
        else
            echo "  Status: loaded but not running"
        fi
    elif [[ -f "$actiond_plist" ]]; then
        actiond_own=true
        local actiond_bin
        actiond_bin="$(extract_plist_bin "$actiond_plist")"
        echo "  Plist:  $actiond_plist"
        echo "  Binary: ${actiond_bin:-unknown}"
        echo "  Status: plist exists but not loaded"
    else
        echo "  Status: not installed"
    fi
    if [[ -n "$actiond_brew" && "$actiond_own" == true ]]; then
        echo "  Conflict: Homebrew and ${ACTIOND_PLIST_LABEL} are both registered"
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

    local skip_lgh=false
    local skip_actiond=false
    if homebrew_blocks_install "lgh" "LGH" "$lgh_unit"; then
        skip_lgh=true
    fi
    if homebrew_blocks_install "actiond" "ActionD" "$actiond_unit"; then
        skip_actiond=true
    fi

    # Check for existing units and collect all conflicts
    local conflicts=()
    local existing_lgh_bin existing_actiond_bin
    existing_lgh_bin="$(extract_unit_bin "$lgh_unit")"
    existing_actiond_bin="$(extract_unit_bin "$actiond_unit")"

    if [[ -f "$lgh_unit" ]] && ! $FORCE && ! $skip_lgh; then
        conflicts+=("LGH unit exists: $lgh_unit (binary: ${existing_lgh_bin:-unknown})")
    fi
    if [[ -f "$actiond_unit" ]] && ! $FORCE && ! $skip_actiond; then
        conflicts+=("ActionD unit exists: $actiond_unit (binary: ${existing_actiond_bin:-unknown})")
    fi

    if [[ ${#conflicts[@]} -gt 0 ]]; then
        {
            echo ""
            printf '\033[1;31m[ERROR]\033[0m Existing service files found:\n'
            for c in "${conflicts[@]}"; do
                echo "  - $c"
            done
            echo ""
            echo "Use --force to overwrite (creates timestamped backup first)."
        } >&2
        exit 1
    fi

    # Determine PATH for each service
    local lgh_path actiond_path
    if [[ -n "$OPT_PATH" ]]; then
        lgh_path="$OPT_PATH"
        actiond_path="$OPT_PATH"
    elif $FORCE; then
        # With --force, preserve existing PATH if present, else use minimal
        local existing_lgh_path existing_actiond_path
        existing_lgh_path="$(extract_unit_path "$lgh_unit")"
        existing_actiond_path="$(extract_unit_path "$actiond_unit")"
        lgh_path="${existing_lgh_path:-$(build_minimal_path "$lgh_bin")}"
        actiond_path="${existing_actiond_path:-$(build_minimal_path "$actiond_bin")}"
    else
        lgh_path="$(build_minimal_path "$lgh_bin")"
        actiond_path="$(build_minimal_path "$actiond_bin")"
    fi

    info "LGH PATH: $lgh_path"
    info "ActionD PATH: $actiond_path"

    # Create directory (only if not dry-run)
    if ! $DRY_RUN; then
        mkdir -p "$SYSTEMD_USER_DIR"
    fi

    local did_install=false

    # LGH
    if $skip_lgh; then
        :
    elif [[ -n "$lgh_bin" ]]; then
        info "Using lgh binary: $lgh_bin"
        if [[ -f "$lgh_unit" ]]; then
            backup_file "$lgh_unit"
        fi
        if $DRY_RUN; then
            info "[dry-run] Would write: $lgh_unit"
            echo "--- Generated unit content ---"
            generate_lgh_unit "$lgh_bin" "$lgh_path" "${OPT_LGH_ENV[@]+"${OPT_LGH_ENV[@]}"}"
            echo "--- End unit content ---"
        else
            generate_lgh_unit "$lgh_bin" "$lgh_path" "${OPT_LGH_ENV[@]+"${OPT_LGH_ENV[@]}"}" > "$lgh_unit"
            systemctl --user daemon-reload
            systemctl --user enable --now "$LGH_SERVICE"
            success "LGH systemd unit installed and enabled"
            did_install=true
        fi
    else
        warn "lgh not found on PATH; skipping LGH systemd unit"
        warn "Use --lgh-bin to specify the binary path"
    fi

    # ActionD
    if $skip_actiond; then
        :
    elif [[ -n "$actiond_bin" ]]; then
        info "Using actiond binary: $actiond_bin"
        if [[ -f "$actiond_unit" ]]; then
            backup_file "$actiond_unit"
        fi
        if $DRY_RUN; then
            info "[dry-run] Would write: $actiond_unit"
            echo "--- Generated unit content ---"
            generate_actiond_unit "$actiond_bin" "$actiond_path" "${OPT_ACTIOND_ENV[@]+"${OPT_ACTIOND_ENV[@]}"}"
            echo "--- End unit content ---"
        else
            generate_actiond_unit "$actiond_bin" "$actiond_path" "${OPT_ACTIOND_ENV[@]+"${OPT_ACTIOND_ENV[@]}"}" > "$actiond_unit"
            systemctl --user daemon-reload
            systemctl --user enable --now "$ACTIOND_SERVICE"
            success "ActionD systemd unit installed and enabled"
            did_install=true
        fi
    else
        warn "actiond not found on PATH; skipping ActionD systemd unit"
        warn "Use --actiond-bin to specify the binary path"
    fi

    # Note about linger for start-at-boot without login
    if $did_install; then
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

    note_homebrew_left "lgh" "LGH"
    note_homebrew_left "actiond" "ActionD"
}

status_linux() {
    info "Checking systemd --user unit status..."
    local lgh_unit="${SYSTEMD_USER_DIR}/${LGH_SERVICE}"
    local actiond_unit="${SYSTEMD_USER_DIR}/${ACTIOND_SERVICE}"
    local lgh_brew="" actiond_brew=""
    local lgh_own=false actiond_own=false

    lgh_brew="$(describe_homebrew_service lgh || true)"
    actiond_brew="$(describe_homebrew_service actiond || true)"

    echo ""
    echo "LGH:"
    if [[ -n "$lgh_brew" ]]; then
        echo "  Homebrew: ${lgh_brew}"
    else
        echo "  Homebrew: not registered"
    fi
    if [[ -f "$lgh_unit" ]]; then
        lgh_own=true
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
    if [[ -n "$lgh_brew" && "$lgh_own" == true ]]; then
        echo "  Conflict: Homebrew and ${LGH_SERVICE} are both registered"
    fi

    echo ""
    echo "ActionD:"
    if [[ -n "$actiond_brew" ]]; then
        echo "  Homebrew: ${actiond_brew}"
    else
        echo "  Homebrew: not registered"
    fi
    if [[ -f "$actiond_unit" ]]; then
        actiond_own=true
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
    if [[ -n "$actiond_brew" && "$actiond_own" == true ]]; then
        echo "  Conflict: Homebrew and ${ACTIOND_SERVICE} are both registered"
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
  --dry-run              Show what would be done without making changes
  --force                Overwrite existing service files (backs up first)
  --ignore-homebrew      Also install our agent when brew services already registered it
  --lgh-bin PATH         Use specified lgh binary instead of PATH lookup
  --actiond-bin PATH     Use specified actiond binary instead of PATH lookup
  --path PATH            Set PATH for both services (colon-separated)
  --lgh-log-dir DIR      LGH log directory (macOS; default: ~/Library/Logs)
  --actiond-log-dir DIR  ActionD log directory (macOS; default: ~/Library/Logs)
  --lgh-env KEY=VALUE    Add environment variable for LGH (can be repeated)
  --actiond-env KEY=VAL  Add environment variable for ActionD (can be repeated)
  --env KEY=VALUE        Add environment variable for both services (can be repeated)
  --log-dir DIR          Log directory for both services (macOS; shorthand)

Uninstall Options:
  --dry-run              Show what would be done without making changes

On macOS: creates user LaunchAgents in ~/Library/LaunchAgents
On Linux: creates systemd --user units in ~/.config/systemd/user

Homebrew:
  install skips a service that brew services already registered, so we do not
  add a second agent. Names checked (Homebrew service.rb): sh.brew.<formula>
  (current default) and legacy homebrew.mxcl.<formula> on macOS /
  homebrew.<formula> on Linux. A match is a plist or unit in the user service
  directory, the root service directory, or a label loaded in launchd / systemd.
  status reports that. --ignore-homebrew installs our agent anyway.
  uninstall does not remove Homebrew's unit.

PATH handling:
  By default, a minimal PATH is computed: binary dir + brew prefix + system paths.
  Use --path to set an explicit PATH for both services.
  With --force, existing PATH from current plist/unit is preserved unless --path given.

Log files (macOS):
  stdout: <log-dir>/<service>.launchd.out
  stderr: <log-dir>/<service>.launchd.err

Restart behavior:
  Services use KeepAlive/SuccessfulExit=false (macOS) or Restart=on-failure (Linux).
  This prevents crash loops when another instance is already running.

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
            --ignore-homebrew)
                IGNORE_HOMEBREW=true
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
            --path)
                [[ $# -ge 2 ]] || error "--path requires a PATH argument"
                OPT_PATH="$2"
                shift 2
                ;;
            --lgh-log-dir)
                [[ $# -ge 2 ]] || error "--lgh-log-dir requires a path argument"
                OPT_LGH_LOG_DIR="$2"
                shift 2
                ;;
            --actiond-log-dir)
                [[ $# -ge 2 ]] || error "--actiond-log-dir requires a path argument"
                OPT_ACTIOND_LOG_DIR="$2"
                shift 2
                ;;
            --log-dir)
                [[ $# -ge 2 ]] || error "--log-dir requires a path argument"
                OPT_LGH_LOG_DIR="$2"
                OPT_ACTIOND_LOG_DIR="$2"
                shift 2
                ;;
            --lgh-env)
                [[ $# -ge 2 ]] || error "--lgh-env requires a KEY=VALUE argument"
                [[ "$2" == *"="* ]] || error "--lgh-env argument must be KEY=VALUE format"
                OPT_LGH_ENV+=("$2")
                shift 2
                ;;
            --actiond-env)
                [[ $# -ge 2 ]] || error "--actiond-env requires a KEY=VALUE argument"
                [[ "$2" == *"="* ]] || error "--actiond-env argument must be KEY=VALUE format"
                OPT_ACTIOND_ENV+=("$2")
                shift 2
                ;;
            --env)
                [[ $# -ge 2 ]] || error "--env requires a KEY=VALUE argument"
                [[ "$2" == *"="* ]] || error "--env argument must be KEY=VALUE format"
                OPT_LGH_ENV+=("$2")
                OPT_ACTIOND_ENV+=("$2")
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
