#!/bin/sh
set -eu

fail() {
    printf '\n%sAgent Relay: %s%s\n' "$red" "$*" "$reset" >&2
    exit 1
}

cleanup() {
    exitCode=$?
    trap - 0
    if [ -n "$tempDir" ]; then rm -rf "$tempDir"; fi
    if [ -n "$lockDir" ]; then rmdir "$lockDir" 2>/dev/null || :; fi
    if [ "$exitCode" -ne 0 ]; then
        printf 'Setup stopped during: %s. Completed steps are retained; fix the error and rerun.\n' "$installStep" >&2
    fi
    exit "$exitCode"
}

setupColors() {
    green=''
    cyan=''
    red=''
    reset=''
    if [ -t 1 ] && [ "${TERM:-dumb}" != dumb ] && [ -z "${NO_COLOR:-}" ]; then
        green=$(printf '\033[32m')
        cyan=$(printf '\033[36m')
        red=$(printf '\033[31m')
        reset=$(printf '\033[0m')
    fi
}

runStep() {
    stepLabel=$1
    shift
    printf '  %s...\n' "$stepLabel"
    if "$@" > "$tempDir/step.log" 2>&1; then
        return 0
    else
        stepCode=$?
        cat "$tempDir/step.log" >&2
        return "$stepCode"
    fi
}

hashFile() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

checkPath() {
    case "$1" in /*) ;; *) fail "Use an absolute path: $1" ;; esac
    if printf '%s' "$1" | LC_ALL=C grep -q '[[:cntrl:]]'; then
        fail 'Paths must not contain control characters.'
    fi
    pathPart=$1
    while [ "$pathPart" != / ]; do
        [ ! -L "$pathPart" ] || fail "Refusing symlink path: $pathPart"
        pathPart=$(dirname "$pathPart")
    done
}

download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 300 --retry 2 --output "$2" "$1" ||
        fail "Download failed: $1. Check connectivity and that the release is public and complete."
}

main() {
    setupColors
    tempDir=''
    lockDir=''
    installStep='preflight'
    trap cleanup 0
    trap 'exit 130' INT
    trap 'exit 143' TERM HUP
    umask 077
    installAction=${1:-install}
    [ "$#" -le 1 ] || fail 'Usage: sh install.sh [--uninstall]'
    case "$installAction" in
        install|--uninstall) ;;
        --help|-h)
            printf '%s\n' 'Usage: sh install.sh [--uninstall]' 'Options via environment: AGENT_RELAY_VERSION (release tag), AGENT_RELAY_BIN_DIR (default ~/.local/bin), AGENT_RELAY_HOME (default ~/.agent-relay), AGENT_RELAY_SERVICE_NAME (default agent-relay).'
            return ;;
        *) fail 'Usage: sh install.sh [--uninstall]' ;;
    esac
    [ "$(id -u)" -ne 0 ] || fail 'Run as your normal login user, without sudo.'
    [ -n "${HOME:-}" ] || fail 'HOME is not set.'
    PATH="$PATH:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
    export PATH
    for toolName in curl awk uname mktemp chmod mkdir mv ln rm rmdir dirname sed cmp grep cat; do
        command -v "$toolName" >/dev/null 2>&1 || fail "Required Unix tool is missing: $toolName"
    done
    command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 || fail 'Install sha256sum or shasum to verify downloads.'
    osName=$(uname -s)
    case "$osName" in Darwin) osName=darwin ;; Linux) osName=linux ;; *) fail "Unsupported OS: $osName (macOS and Linux only)." ;; esac
    cpuArch=$(uname -m)
    case "$cpuArch" in arm64|aarch64) cpuArch=arm64 ;; x86_64|amd64) cpuArch=amd64 ;; *) fail "Unsupported CPU architecture: $cpuArch" ;; esac
    binDir=${AGENT_RELAY_BIN_DIR:-"$HOME/.local/bin"}
    relayHome=${AGENT_RELAY_HOME:-"$HOME/.agent-relay"}
    serviceName=${AGENT_RELAY_SERVICE_NAME:-agent-relay}
    checkPath "$binDir"
    checkPath "$relayHome"
    case "$serviceName" in ''|[!a-z]*|*[!a-z0-9-]*) fail 'Invalid service name; use a lowercase letter followed by lowercase letters, digits or hyphens.' ;; esac
    mkdir -p "$binDir"
    binDir=$(cd "$binDir" && pwd -P)
    binaryPath="$binDir/agent-relay"
    receiptPath="$binDir/.agent-relay-receipt"
    mkdir "$binDir/.agent-relay-install-lock" 2>/dev/null || fail "Installation is locked at $binDir/.agent-relay-install-lock. If interrupted, confirm no installer is running before removing this empty directory."
    lockDir="$binDir/.agent-relay-install-lock"
    tempDir=$(mktemp -d "$binDir/.agent-relay-download.XXXXXXXX")
    if [ -e "$binaryPath" ] || [ -L "$binaryPath" ] || [ -e "$receiptPath" ] || [ -L "$receiptPath" ]; then
        [ -f "$binaryPath" ] && [ ! -L "$binaryPath" ] && [ -f "$receiptPath" ] && [ ! -L "$receiptPath" ] || fail 'Existing executable or receipt is not a managed regular file. Move it aside after review; nothing was overwritten.'
        oldHash=$(hashFile "$binaryPath")
        printf '%s\n%s\n%s\n' "$oldHash" "$relayHome" "$serviceName" > "$tempDir/expected-receipt"
        cmp -s "$receiptPath" "$tempDir/expected-receipt" || fail 'Existing executable, home, or service does not match the installer receipt. Rerun with the original settings or review the files manually.'
    fi
    if [ "$installAction" = --uninstall ]; then
        installStep='uninstall'
        if [ ! -e "$binaryPath" ]; then
            printf 'Agent Relay is not installed at %s.\n' "$binaryPath"
            return
        fi
        runStep "Disconnecting clients" "$binaryPath" setup --home "$relayHome" --remove --if-present || fail 'MCP removal failed. Review the reported conflict; the executable and service are retained.'
        runStep "Removing background service" "$binaryPath" service uninstall --name "$serviceName" || fail 'Service removal failed. The executable is retained for recovery.'
        rm "$binaryPath" "$receiptPath"
        printf '\nAgent Relay uninstalled. Data, identities, logs, and private backups are preserved at %s and the documented service/client locations.\n' "$relayHome"
        return
    fi
    tailscalePath=$(command -v tailscale || :)
    if [ -z "$tailscalePath" ] && [ -x /Applications/Tailscale.app/Contents/MacOS/Tailscale ]; then
        tailscalePath=/Applications/Tailscale.app/Contents/MacOS/Tailscale
    fi
    [ -n "$tailscalePath" ] || fail 'Tailscale is missing. Install it from https://tailscale.com/download, connect it, then rerun.'
    "$tailscalePath" status --json > "$tempDir/tailscale.json" || fail 'Cannot query Tailscale. Start Tailscale, sign in, and rerun.'
    grep -Eq '"BackendState"[[:space:]]*:[[:space:]]*"Running"' "$tempDir/tailscale.json" || fail 'Tailscale is not connected. Run tailscale up or sign in through the Tailscale app.'
    "$tailscalePath" ip -4 > "$tempDir/tailscale-ip" || fail 'Tailscale has no IPv4 address. Connect it and rerun.'
    [ -s "$tempDir/tailscale-ip" ] || fail 'Enable a Tailscale IPv4 address before installing.'
    if [ "$osName" = darwin ]; then
        launchctl print "gui/$(id -u)" >/dev/null 2>&1 || fail 'Log into the macOS desktop as this user, then rerun (LaunchAgents need a GUI login).'
    else
        command -v systemctl >/dev/null 2>&1 || fail 'Linux requires systemd user services.'
        systemctl --user show-environment >/dev/null 2>&1 || fail 'No systemd user manager. Run from your normal login session.'
    fi
    installStep='download and checksum verification'
    releaseVersion=${AGENT_RELAY_VERSION:-}
    if [ -z "$releaseVersion" ]; then
        releaseUrl=$(curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 60 --retry 2 --output /dev/null --write-out '%{url_effective}' https://github.com/echo1097/agent-relay/releases/latest) || fail 'Cannot resolve the latest GitHub Release. Check connectivity or set AGENT_RELAY_VERSION to an existing release tag.'
        case "$releaseUrl" in https://github.com/echo1097/agent-relay/releases/tag/*) releaseVersion=${releaseUrl##*/} ;; *) fail 'GitHub did not return a release tag.' ;; esac
    fi
    case "$releaseVersion" in v[0-9]*) ;; *) fail 'Release tags must start with v followed by a digit.' ;; esac
    case "$releaseVersion" in *[!a-zA-Z0-9._-]*) fail 'Invalid release tag.' ;; esac
    assetName="agent-relay_${osName}_${cpuArch}"
    releaseBase="https://github.com/echo1097/agent-relay/releases/download/$releaseVersion"
    printf '\n%sAgent Relay %s%s\n' "$cyan" "$releaseVersion" "$reset"
    printf '  Downloading for %s %s...\n' "$osName" "$cpuArch"
    download "$releaseBase/$assetName" "$tempDir/agent-relay"
    download "$releaseBase/SHA256SUMS" "$tempDir/SHA256SUMS"
    expectedHash=$(awk -v assetName="$assetName" '$2 == assetName { count++; value=$1 } END { if (count != 1 || length(value) != 64 || value ~ /[^0-9a-f]/) exit 1; print value }' "$tempDir/SHA256SUMS") || fail 'The checksum manifest has no unique valid entry for this binary.'
    actualHash=$(hashFile "$tempDir/agent-relay")
    [ "$actualHash" = "$expectedHash" ] || fail 'Checksum mismatch. The download was not executed or installed; retry or report the release.'
    chmod 700 "$tempDir/agent-relay"
    binaryVersion=$("$tempDir/agent-relay" version) || fail 'The verified binary cannot run on this machine.'
    [ "$binaryVersion" = "agent-relay $releaseVersion" ] || fail 'Binary version does not match the selected release.'
    installStep='binary installation'
    printf '%s\n%s\n%s\n' "$actualHash" "$relayHome" "$serviceName" > "$tempDir/receipt"
    if [ -e "$binaryPath" ]; then
        [ "$(hashFile "$binaryPath")" = "$oldHash" ] || fail 'Installed binary changed during download; retry after reviewing it.'
        mv -f "$tempDir/agent-relay" "$binaryPath"
        mv -f "$tempDir/receipt" "$receiptPath"
    else
        ln "$tempDir/agent-relay" "$binaryPath" || fail 'Executable path was created concurrently; nothing was overwritten.'
        ln "$tempDir/receipt" "$receiptPath" || fail 'Receipt path was created concurrently. Review the installation before retrying.'
    fi
    installStep='local initialization'
    runStep "Preparing local data" "$binaryPath" status --home "$relayHome" || fail 'Initialization failed; preserve existing data and follow the error above.'
    nodeId=$(awk '/^  node_[a-zA-Z0-9-]+$/ { print $1; exit }' "$tempDir/step.log")
    [ -n "$nodeId" ] || fail 'Could not read the local node ID. Run agent-relay status to inspect the installation.'
    installStep='background service installation'
    runStep "Starting background service" "$binaryPath" service install --home "$relayHome" --name "$serviceName" || fail 'Service startup failed. Check the reported logs and any existing daemon or port conflict. No unowned process was stopped.'
    installStep='MCP client configuration'
    runStep "Connecting MCP clients" "$binaryPath" setup --home "$relayHome" --if-present || fail 'Client configuration failed. Review the conflict and use setup --replace only if you intend to replace that entry.'
    installStep='doctor'
    runStep "Checking installation" "$binaryPath" doctor --home "$relayHome" --installation || fail 'Installation needs attention. Follow doctor remediation above, then rerun.'
    printf '\n%sAgent Relay %s installed successfully.%s\n' "$green" "$releaseVersion" "$reset"
    printf 'Background service runs at login. Reconnect Codex or Claude to load Relay tools.\n'
    case ":$PATH:" in *":$binDir:"*) ;; *) printf 'Add this directory to your shell PATH: %s\n' "$binDir" ;; esac
    printf '\nDevices on your tailnet pair automatically. Explicitly blocked nodes stay blocked.\n'
    printf '\n%sThis computer’s node ID:%s\n%s\n' "$cyan" "$reset" "$nodeId"

}

main "$@"
