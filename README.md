# audiorelay

Lightweight live audio relay: pulls an upstream live stream (typically HLS),
extracts its audio, transcodes it to low-bitrate Opus, and publishes it as an
Ogg Opus HTTP stream for mobile clients.

Current state: **partial skeleton**. Command-line configuration parsing and
validation are implemented; the ffmpeg pipeline and the supervision loop are
still stubs that return `not implemented`.

## Design target

```
upstream HLS ──► ffmpeg (demux / decode / Opus encode / Ogg mux) ──► Icecast ──► client
                        ▲                                              ▲
                        └──────────── Go control plane ───────────────┘
```

- Media plane: ffmpeg does all audio processing; Icecast does HTTP broadcast.
- Control plane: Go validates configuration and supervises the pipeline.
- Wire format: `Opus → Ogg → HTTP/1.1 → TCP → IPv6`, 32 kbps stereo by default.

See [docs/architecture.md](docs/architecture.md) for the reasoning and the
planned extensions.

## Layout

| Path | Purpose |
|---|---|
| `cmd/audiorelay/` | CLI entry point, wiring only |
| `internal/config/` | configuration types, parsing, validation and tests |
| `internal/pipeline/` | media plane boundary + ffmpeg backend (stub) |
| `internal/relay/` | control plane: supervision loop (stub) |
| `nix/package.nix` | Go package build, ffmpeg wrapped onto `PATH` |
| `examples/icecast.xml` | development Icecast configuration |
| `docs/` | architecture notes |

## Development

```sh
nix develop          # Go, gopls, ffmpeg, icecast, curl
go build ./...
go vet ./...
go run ./cmd/audiorelay -h
```

Nix build and run:

```sh
nix build            # ./result/bin/audiorelay
nix run              # same binary via the flake app
```

Note: `nix build` only sees files tracked by Git (or, outside a Git
repository, the whole directory tree). Track new sources before building in a
Git checkout.

## Planned milestones

1. MVP: ffmpeg + Icecast relaying one upstream HLS audio track.
2. Go control plane: flag parsing, restart with backoff, structured logs.
3. NixOS service module (`nix/module.nix`) and per-architecture verification.
4. Optional: in-process media backend, native HTTP broadcast sink, multi-channel.

## License

BSD 3-Clause License. See [LICENSE.txt](LICENSE.txt).
