#!/usr/bin/env bash

set -euo pipefail

readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly CYAN='\033[0;36m'
readonly BOLD='\033[1m'
readonly NC='\033[0m'

REPO="thirawat27/wut"
VERSION="latest"
FORCE=false
UNINSTALL=false
NO_INIT=false
NO_SHELL=false

print_header() {
    echo -e "${CYAN}${BOLD}"
    echo ' _    _ _____ _____'
    echo '| |  | |_   _|  __ \'
    echo '| |  | | | | | |  | |'
    echo '| |  | | | | | |  | |'
    echo '| |__| |_| |_| |__| |'
    echo ' \____/|_____|_____/'
    echo -e "${NC}"
    echo -e "${BLUE}AI-Powered Command Helper${NC}"
    echo ""
}

info()    { echo -e "${BLUE}[INFO]${NC}  $1"; }
success() { echo -e "${GREEN}[OK]${NC}    $1"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $1"; }
error()   { echo -e "${RED}[ERROR]${NC} $1" >&2; }
die()     { error "$1"; exit 1; }

usage() {
    cat <<'EOF'
Usage: install.sh [OPTIONS]

Options:
    -v, --version TAG   Install a specific release tag (default: latest)
    -f, --force         Skip overwrite confirmation
    --uninstall         Remove the installed binary
    --no-init           Skip automatic `wut init --quick`
    --no-shell          Skip shell hook installation during init (default: skipped)
    -h, --help          Show this message

Examples:
    curl -fsSL https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.sh | bash
    curl -fsSL https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.sh | bash -s -- --version v0.3.0
EOF
}

http_get() {
    local url="$1"
    local out="$2"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --progress-bar "$url" -o "$out"
    elif command -v wget >/dev/null 2>&1; then
        wget -q --show-progress "$url" -O "$out"
    else
        die "Neither curl nor wget found. Please install one of them."
    fi
}

http_get_json() {
    local url="$1"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -H "User-Agent: WUT-Installer" -H "Accept: application/vnd.github+json" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- --header="User-Agent: WUT-Installer" --header="Accept: application/vnd.github+json" "$url"
    else
        die "Neither curl nor wget found. Please install one of them."
    fi
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -v|--version)
                VERSION="${2:-}"; shift 2 ;;
            -f|--force)
                FORCE=true; shift ;;
            --uninstall)
                UNINSTALL=true; shift ;;
            --no-init)
                NO_INIT=true; shift ;;
            --no-shell)
                NO_SHELL=true; shift ;;
            -h|--help)
                usage; exit 0 ;;
            *)
                error "Unknown option: $1"
                usage
                exit 1 ;;
        esac
    done
}

detect_os() {
    case "$(uname -s)" in
        Linux) echo "Linux" ;;
        Darwin) echo "Darwin" ;;
        FreeBSD) echo "Freebsd" ;;
        OpenBSD) echo "Openbsd" ;;
        NetBSD) echo "Netbsd" ;;
        *)
            die "Unsupported OS: $(uname -s)"
            ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "x86_64" ;;
        aarch64|arm64) echo "arm64" ;;
        i386|i686) echo "i386" ;;
        armv6l) echo "armv6" ;;
        armv7l|armv7) echo "armv7" ;;
        riscv64) echo "riscv64" ;;
        *)
            die "Unsupported architecture: $(uname -m)"
            ;;
    esac
}

resolve_release_json() {
    local version="$1"
    local api_url

    if [ "$version" = "latest" ]; then
        api_url="https://api.github.com/repos/${REPO}/releases/latest"
    else
        local tag="${version}"
        [[ "$tag" == v* ]] || tag="v${tag}"
        api_url="https://api.github.com/repos/${REPO}/releases/tags/${tag}"
    fi

    info "Querying GitHub API: $api_url"
    http_get_json "$api_url"
}

extract_json_field() {
    local json="$1"
    local field="$2"
    printf '%s' "$json" | sed -n "s/.*\"${field}\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" | head -n1
}

find_asset_url() {
    local json="$1"
    local asset_name="$2"

    printf '%s\n' "$json" | awk -v asset="$asset_name" '
        $0 ~ "\"name\": \"" asset "\"" { found=1; next }
        found && /"browser_download_url"/ {
            gsub(/.*"browser_download_url"[[:space:]]*:[[:space:]]*"/, "", $0)
            gsub(/".*/, "", $0)
            print
            exit
        }
    '
}

choose_install_dir() {
    if [ -w "/usr/local/bin" ]; then
        echo "/usr/local/bin"
        return
    fi

    mkdir -p "${HOME}/.local/bin"
    echo "${HOME}/.local/bin"
}

run_quick_init() {
    local binary_path="$1"
    if [ "$NO_INIT" = true ]; then
        return
    fi

    local args=(init --quick --skip-shell)
    if [ "$NO_SHELL" = true ]; then
        # Kept for backwards compatibility; --skip-shell is now the default.
        args+=(--skip-shell)
    fi

    info "Running first-time setup automatically..."
    if "$binary_path" "${args[@]}"; then
        success "WUT initialized"
    else
        warn "Automatic initialization failed. You can run '$binary_path init' manually."
    fi
}

uninstall_wut() {
    local removed=false
    local candidates=(
        "$(command -v wut 2>/dev/null || true)"
        "/usr/local/bin/wut"
        "${HOME}/.local/bin/wut"
    )

    for candidate in "${candidates[@]}"; do
        if [ -n "$candidate" ] && [ -f "$candidate" ]; then
            rm -f "$candidate"
            removed=true
            success "Removed $candidate"
        fi
    done

    if [ "$removed" = false ]; then
        warn "No installed wut binary was found."
    fi
}

main() {
    parse_args "$@"
    print_header

    if [ "$UNINSTALL" = true ]; then
        uninstall_wut
        exit 0
    fi

    if command -v wut >/dev/null 2>&1 && [ "$FORCE" != true ]; then
        local current_ver
        current_ver="$(wut --version 2>/dev/null | head -1 || echo "unknown")"
        warn "WUT is already installed (${current_ver})"
        read -rp "Reinstall / upgrade? [y/N] " answer
        [[ "$answer" =~ ^[Yy]$ ]] || { info "Cancelled."; exit 0; }
    fi

    local os_name arch_name release_json tag_name asset_name asset_url
    os_name="$(detect_os)"
    arch_name="$(detect_arch)"
    release_json="$(resolve_release_json "$VERSION")"
    tag_name="$(extract_json_field "$release_json" "tag_name")"
    [ -n "$tag_name" ] || die "Could not determine release version from GitHub API"

    asset_name="wut_${tag_name}_${os_name}_${arch_name}.tar.gz"
    asset_url="$(find_asset_url "$release_json" "$asset_name")"
    [ -n "$asset_url" ] || die "Asset '${asset_name}' not found in release ${tag_name}"

    local temp_dir archive_path extract_dir
    temp_dir="$(mktemp -d)"
    archive_path="${temp_dir}/${asset_name}"
    extract_dir="${temp_dir}/extract"
    mkdir -p "$extract_dir"
    trap 'rm -rf "$temp_dir"' EXIT

    info "Downloading ${asset_name}"
    http_get "$asset_url" "$archive_path"

    info "Extracting archive"
    tar -xzf "$archive_path" -C "$extract_dir"

    local binary_path
    binary_path="$(find "$extract_dir" -type f -name wut | head -n1)"
    [ -n "$binary_path" ] || die "Could not find extracted wut binary"

    local install_dir final_path
    install_dir="$(choose_install_dir)"
    final_path="${install_dir}/wut"
    install -m 0755 "$binary_path" "$final_path"
    success "Installed to ${final_path}"

    run_quick_init "$final_path"

    echo ""
    echo -e "${GREEN}${BOLD}Installation complete!${NC}"
    echo "  ${final_path} --help"
    echo "  ${final_path} suggest"
}

main "$@"
