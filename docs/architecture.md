# audiorelay architecture

## Goal

Relay the audio track of an upstream live TV stream to mobile listeners over
cellular networks with minimal bandwidth, using only standard, publicly
specified protocols.

Constraints that drive the design:

- Upstream is usually HLS (`m3u8` + MPEG-TS or fMP4), possibly DRM-free IPTV.
- Downstream is a phone on 4G/5G; bandwidth matters more than latency.
- Clients should be existing players (VLC for Android first), not custom apps.
- Deployment target is NixOS on several architectures.
- The first demo must be small; the boundaries must allow later replacement.

## Layering

```
┌─────────────────────────────────────────────────────────────┐
│ client (VLC / mpv): HTTP GET http://[addr]:8000/tv.opus     │
└───────────────────────────────▲─────────────────────────────┘
                                │ Ogg Opus over HTTP/1.1 over TCP/IPv6
┌───────────────────────────────┴─────────────────────────────┐
│ Icecast: mountpoint, fan-out, per-client buffering           │
└───────────────────────────────▲─────────────────────────────┘
                                │ icecast:// source protocol
┌───────────────────────────────┴─────────────────────────────┐
│ pipeline (media plane): ffmpeg                                │
│   demux → decode audio → resample → libopus encode → Ogg mux  │
└───────────────────────────────▲─────────────────────────────┘
                                │ process supervision, exit codes
┌───────────────────────────────┴─────────────────────────────┐
│ relay (control plane): config → pipeline → restart policy     │
└─────────────────────────────────────────────────────────────┘
```

Three independent failure domains: the upstream stream, the transcoder, the
distribution server. Keeping them separate is what makes the demo simple and
the later evolution cheap.

### Why Icecast in the first version

A correct Ogg Opus live endpoint is not "pipe ffmpeg stdout to every socket".
New listeners need `OpusHead`/`OpusTags`, must start on an Ogg page boundary,
slow clients must not stall the encoder, and the buffer must stay bounded so
latency does not creep. Icecast already implements this. The Go program stays a
supervisor until there is a concrete reason to reimplement broadcast.

### Wire format decision

`Opus → Ogg (RFC 7845) → HTTP/1.1 → TCP → IPv6`, 32 kbps VBR stereo,
48 kHz, 20 ms frames.

- Opus is a codec (RFC 6716), not an application-layer protocol; Ogg Opus is
  the container and HTTP is the delivery mechanism.
- TCP vs UDP does not meaningfully change bitrate at these rates; upstream HLS
  already dominates end-to-end latency, so RTP/WebRTC buys little here.
- 32 kbps stereo is a starting point for TV audio, not a guarantee of quality.
  Expect to compare 24/32/40/48 kbps by ear before settling.
- HTTPS is deferred for the LAN demo; the switch is a deployment change, not a
  redesign.

## Packages

| Package | Responsibility | Must not know about |
|---|---|---|
| `internal/config` | `Config` struct, parsing, validation | ffmpeg flags, Icecast internals |
| `internal/pipeline` | `Spec`, `Pipeline` interface, `Backend`, ffmpeg backend | listeners, HTTP, restart policy |
| `internal/relay` | lifecycle, restart policy, logging | media formats, transport details |
| `cmd/audiorelay` | flag wiring, signal handling, exit codes | anything but composition |

Interfaces are defined at the point of use and kept small so implementations
can be swapped without touching callers.

### pipeline contract

```go
type Pipeline interface {
    Start(ctx context.Context) error
    Wait() error
    Stop(ctx context.Context) error
}
```

`Start` returns promptly; `Wait` reports the terminal result; `Stop` is
idempotent and safe after exit. Cancelling the `Start` context is equivalent to
`Stop`. The backend owns its own process management, stderr capture and error
mapping, so the relay never inspects ffmpeg output directly.

### relay contract

`Run(ctx)` owns the restart loop: map `Config` → `pipeline.Spec`, start, wait,
and on backend construction, start, or unexpected exit retry with capped
exponential backoff. The production defaults are a 1-second initial delay,
a 30-second maximum delay, and a 5-second stop timeout. Cancellation stops the
current pipeline and returns `nil`, so the CLI can distinguish operator-
initiated stop from failure.

## Configuration surface (first version)

| Flag | Meaning | Default |
|---|---|---|
| `-input` | upstream media URL | required |
| `-output` | sink URL (Icecast source URL) | required |
| `-bitrate` | Opus target bitrate, `6k`–`510k` | `32k` |
| `-channels` | output channels, `1` or `2` | `2` |
| `-sample-rate` | output sample rate: 8000, 12000, 16000, 24000, or 48000 Hz | `48000` |

`-input` and `-output` must be absolute URLs. The configuration package does
not restrict their schemes: choosing a supported upstream protocol and sink
remains the responsibility of the selected pipeline backend.

Later: config file (multiple channels), environment/systemd credentials for
passwords, `-log-level`.

Known gap: the current CLI shape puts the Icecast source password in
`argv`. Before any non-local deployment this must move to a file or systemd
credential, not only for secrecy but because `ps` output is world-readable on
many systems.

## Nix packaging

- `buildGoModule` with `subPackages = [ "cmd/audiorelay" ]`.
- `CGO_ENABLED=0` while no native libraries are linked.
- `makeWrapper` prepends ffmpeg to `PATH`; Icecast stays a separate service so
  it can be restarted and configured independently.
- `systems` starts with `x86_64-linux` and `aarch64-linux`. Go cross-compiles
  easily, but nixpkgs coverage for ffmpeg/Icecast on `riscv64-linux` and
  `loongarch64-linux` must be verified before those are added to the matrix.
- Low-power boards can run Icecast only, with transcoding on a stronger host,
  because the two are already separate processes.

## Deliberate non-goals for the demo

- No web UI, database, accounts or multi-tenant auth.
- No adaptive bitrate ladders or per-listener transcoding.
- No HLS output, no video passthrough.
- No custom HTTP broadcast implementation.
- No on-demand start/stop of the upstream fetch (nice, but needs session
  tracking).

## Open questions

1. Whether to distinguish persistent upstream errors such as HTTP 404 from
   transient failures such as timeouts; the current policy retries both with
   the same capped backoff.
2. Whether to keep Icecast long-term or migrate to a Go broadcast sink; the
   answer depends on measured listener counts and latency requirements.
3. IPv6 only, or dual-stack? The demo targets ULA/link-local-free addresses;
   phones outside the LAN need WireGuard/Tailscale or a public address.
4. Background playback behaviour of candidate Android players with an
   unbounded Ogg Opus HTTP stream — needs measurement, not documentation
   reading.
