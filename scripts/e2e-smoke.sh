#!/usr/bin/env bash
set -Eeuo pipefail

# Run a deterministic local end-to-end smoke test:
# synthetic AAC/HLS source -> audiorelay -> Icecast -> captured Ogg Opus.
#
# Run from a nix develop shell so go, ffmpeg, icecast, curl and python3 are
# available:
#
#   nix develop --command ./scripts/e2e-smoke.sh
#
# Set AUDIORELAY_BIN to test an already-built binary instead of rebuilding it.

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
RUNTIME_DIR=$(mktemp -d "${TMPDIR:-/tmp}/audiorelay-e2e.XXXXXX")
SOURCE_DIR="$RUNTIME_DIR/source"
ICECAST_LOG_DIR="$RUNTIME_DIR/icecast-log"
mkdir -p "$SOURCE_DIR" "$ICECAST_LOG_DIR"

SOURCE_PORT=$(python3 - <<'PY'
import socket
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)
ICECAST_PORT=$(python3 - <<'PY'
import socket
with socket.socket(socket.AF_INET6, socket.SOCK_STREAM) as sock:
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind(("::1", 0))
    print(sock.getsockname()[1])
PY
)

SOURCE_PID=
HTTP_PID=
ICECAST_PID=
RELAY_PID=

cleanup() {
    set +e
    for pid in "$RELAY_PID" "$ICECAST_PID" "$HTTP_PID" "$SOURCE_PID"; do
        if [[ -n "$pid" ]]; then
            kill "$pid" 2>/dev/null || true
        fi
    done
    for pid in "$RELAY_PID" "$ICECAST_PID" "$HTTP_PID" "$SOURCE_PID"; do
        if [[ -n "$pid" ]]; then
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [[ "${AUDIORELAY_KEEP_RUNTIME:-0}" == 1 ]]; then
        echo "e2e-smoke: keeping runtime directory $RUNTIME_DIR" >&2
    else
        rm -rf "$RUNTIME_DIR"
    fi
}
trap cleanup EXIT

fail() {
    echo "e2e-smoke: $*" >&2
    echo "--- relay.log ---" >&2
    cat "$RUNTIME_DIR/relay.log" 2>/dev/null || true
    echo "--- icecast.log ---" >&2
    cat "$RUNTIME_DIR/icecast.log" 2>/dev/null || true
    echo "--- source.log ---" >&2
    cat "$RUNTIME_DIR/source.log" 2>/dev/null || true
    exit 1
}

wait_for_file() {
    local path=$1
    local attempts=100
    while (( attempts-- > 0 )); do
        if [[ -s "$path" ]]; then
            return 0
        fi
        sleep 0.1
    done
    return 1
}

wait_for_port() {
    local url=$1
    local attempts=100
    while (( attempts-- > 0 )); do
        if curl --noproxy '*' -sS --max-time 1 -o /dev/null "$url" 2>/dev/null; then
            return 0
        fi
        sleep 0.1
    done
    return 1
}

wait_for_url() {
    local url=$1
    local attempts=100
    local status
    while (( attempts-- > 0 )); do
        status=$(curl --noproxy '*' -sS --max-time 1 -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || true)
        if [[ "$status" == 2* ]]; then
            return 0
        fi
        sleep 0.1
    done
    return 1
}

export NO_PROXY="127.0.0.1,localhost,::1,[::1]${NO_PROXY:+,$NO_PROXY}"
export no_proxy="$NO_PROXY"

# Generate an infinite, real-time AAC audio-only HLS source. This avoids
# depending on an external broadcaster and makes the test repeatable offline.
ffmpeg -hide_banner -loglevel error -nostdin \
    -re -f lavfi -i 'sine=frequency=1000:sample_rate=48000' \
    -c:a aac -b:a 64k \
    -f hls -hls_time 1 -hls_list_size 6 \
    -hls_flags delete_segments+append_list+omit_endlist \
    -hls_segment_filename "$SOURCE_DIR/segment%06d.ts" \
    "$SOURCE_DIR/index.m3u8" >"$RUNTIME_DIR/source.log" 2>&1 &
SOURCE_PID=$!

python3 -m http.server "$SOURCE_PORT" --bind 127.0.0.1 --directory "$SOURCE_DIR" \
    >"$RUNTIME_DIR/http.log" 2>&1 &
HTTP_PID=$!

wait_for_file "$SOURCE_DIR/index.m3u8" || fail "local HLS playlist was not generated"

# Use a temporary copy so the smoke test can select a free port and disable
# Icecast's connection burst. The example keeps the burst for normal playback,
# while this test measures the steady-state audio bitrate.
ICECAST_CONFIG="$RUNTIME_DIR/icecast.xml"
sed \
    -e "s|<port>8000</port>|<port>$ICECAST_PORT</port>|" \
    -e "s|<logdir>./var/log/icecast</logdir>|<logdir>$ICECAST_LOG_DIR</logdir>|" \
    -e 's|<burst-size>65535</burst-size>|<burst-size>0</burst-size>|' \
    "$ROOT_DIR/examples/icecast.xml" >"$ICECAST_CONFIG"

icecast -c "$ICECAST_CONFIG" >"$RUNTIME_DIR/icecast.log" 2>&1 &
ICECAST_PID=$!
wait_for_port "http://[::1]:$ICECAST_PORT/" || fail "Icecast did not start"

if [[ -n "${AUDIORELAY_BIN:-}" ]]; then
    RELAY_COMMAND=("$AUDIORELAY_BIN")
else
    AUDIORELAY_BUILD="$RUNTIME_DIR/audiorelay"
    (cd "$ROOT_DIR" && go build -o "$AUDIORELAY_BUILD" ./cmd/audiorelay) \
        || fail "could not build audiorelay"
    RELAY_COMMAND=("$AUDIORELAY_BUILD")
fi

PASSWORD_FILE="$RUNTIME_DIR/source-password"
printf '%s\n' 'change-me-source' >"$PASSWORD_FILE"
chmod 600 "$PASSWORD_FILE"

"${RELAY_COMMAND[@]}" \
    -input "http://127.0.0.1:$SOURCE_PORT/index.m3u8" \
    -output "icecast://source@[::1]:$ICECAST_PORT/tv.opus" \
    -source-password-file "$PASSWORD_FILE" \
    -log-level debug >"$RUNTIME_DIR/relay.log" 2>&1 &
RELAY_PID=$!

MOUNT_URL="http://[::1]:$ICECAST_PORT/tv.opus"
wait_for_url "$MOUNT_URL" || fail "audiorelay did not publish the Icecast mount"

# The password file must never be transformed into an ffmpeg command-line
# argument. Go may start ffmpeg from any runtime thread, so inspect children
# of every relay thread rather than only the main thread. The Go Icecast client
# owns authentication and ffmpeg only writes Ogg bytes to pipe:1.
FFMPEG_PID=
for children_file in /proc/"$RELAY_PID"/task/*/children; do
    [[ -r "$children_file" ]] || continue
    for child in $(<"$children_file"); do
        [[ -r "/proc/$child/comm" ]] || continue
        if [[ $(<"/proc/$child/comm") == ffmpeg ]]; then
            FFMPEG_PID=$child
            break 2
        fi
    done
done
[[ -n "$FFMPEG_PID" ]] || fail "could not find the relay's ffmpeg process"

command_line=$(tr '\0' ' ' <"/proc/$FFMPEG_PID/cmdline")
if [[ "$command_line" == *"change-me-source"* || "$command_line" == *"icecast://"* ]]; then
    fail "ffmpeg command line exposes Icecast credentials: $command_line"
fi

CAPTURE="$RUNTIME_DIR/capture.ogg"
CAPTURE_SECONDS=${AUDIORELAY_CAPTURE_SECONDS:-10}
start_time=$(date +%s.%N)
set +e
curl --noproxy '*' -sS --max-time "$CAPTURE_SECONDS" "$MOUNT_URL" -o "$CAPTURE" 2>/dev/null
curl_status=$?
set -e
end_time=$(date +%s.%N)

# curl normally exits with 28 because a live stream has no EOF. Any other
# failure means the stream was not readable.
if [[ "$curl_status" -ne 0 && "$curl_status" -ne 28 ]]; then
    fail "stream capture failed with curl status $curl_status"
fi

[[ -s "$CAPTURE" ]] || fail "stream capture is empty"

if ! printf 'OggS' | cmp -n 4 - "$CAPTURE" >/dev/null 2>&1; then
    fail "capture does not start with an OggS page"
fi

ffprobe_output=$(ffprobe -hide_banner -v error \
    -show_entries stream=codec_name,channels,sample_rate \
    -of default=noprint_wrappers=1 "$CAPTURE" 2>&1) \
    || fail "ffprobe could not decode the captured stream"

grep -q '^codec_name=opus$' <<<"$ffprobe_output" \
    || fail "captured stream is not Opus: $ffprobe_output"
grep -q '^channels=2$' <<<"$ffprobe_output" \
    || fail "captured stream is not stereo: $ffprobe_output"
grep -q '^sample_rate=48000$' <<<"$ffprobe_output" \
    || fail "captured stream is not 48 kHz: $ffprobe_output"

bytes=$(wc -c <"$CAPTURE")
elapsed=$(awk -v start="$start_time" -v end="$end_time" 'BEGIN { print end - start }')
bitrate=$(awk -v bytes="$bytes" -v seconds="$elapsed" 'BEGIN { print bytes * 8 / seconds / 1000 }')

# The target is 32 kbit/s. Unconstrained Opus VBR can use substantially more
# than the target for synthetic signals, so keep the smoke range broad enough
# to avoid making content-dependent bitrate a false failure while still
# catching a missing/incorrect encoder setting.
if ! awk -v rate="$bitrate" 'BEGIN { exit !(rate >= 20 && rate <= 100) }'; then
    fail "measured bitrate ${bitrate} kbit/s is outside the expected 20-100 kbit/s range"
fi

printf 'e2e-smoke: PASS\n'
printf '  source:   local AAC/HLS on 127.0.0.1:%s\n' "$SOURCE_PORT"
printf '  sink:     Icecast Ogg Opus on [::1]:%s/tv.opus\n' "$ICECAST_PORT"
printf '  capture:  %d bytes over %.1f seconds\n' "$bytes" "$elapsed"
printf '  bitrate:  %.1f kbit/s\n' "$bitrate"
printf '  stream:   %s\n' "${ffprobe_output//$'\n'/, }"
