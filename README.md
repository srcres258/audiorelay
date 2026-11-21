# audiorelay

Lightweight live audio relay: pulls an upstream live stream (typically HLS),
extracts its audio, transcodes it to low-bitrate Opus, and publishes it as an
Ogg Opus HTTP stream for mobile clients.

Current state: **working demo**. Command-line configuration and validation,
the ffmpeg pipeline backend, and supervision with capped restart backoff are
implemented and verified end to end against Icecast by `scripts/e2e-smoke.sh`
and CI. Permanent failures, such as invalid Icecast credentials or an
upstream HTTP 4xx that prevents opening the input, stop instead of retrying.
Structured logs are emitted to stderr and can be filtered with `-log-level`.

Known gaps: listeners are disconnected whenever the pipeline restarts and
players such as VLC for Android do not reconnect automatically. A brief
connection-level upstream outage still terminates ffmpeg: its HLS demuxer
has no retry for a failed playlist reload and does not receive the HTTP
reconnect options for the playlist and segment requests it opens itself, so
the relay can only shorten the resulting mount gap by restarting quickly.
Upstream errors that only appear as ffmpeg stderr text are classified
conservatively.

## Design target

```
upstream HLS ──► ffmpeg (demux / decode / Opus / Ogg) ──► Go Icecast source ──► Icecast ──► client
                        ▲                              ▲                              ▲
                        └──────────────────────────────┴──── Go control plane ──────┘
```

- Media plane: ffmpeg does audio processing; Go publishes its Ogg stdout to
  Icecast; Icecast does HTTP listener broadcast.
- Control plane: Go validates configuration and supervises the pipeline.
- Wire format: `Opus → Ogg → HTTP/1.1 → TCP → IPv6`, 32 kbps stereo by default.

See [docs/architecture.md](docs/architecture.md) for the reasoning and the
planned extensions. GitHub Actions runs formatting, Go tests (including the
race detector), vet, the deterministic smoke test, flake checks and the Nix
package build on pushes and pull requests.

## Layout

| Path | Purpose |
|---|---|
| `cmd/audiorelay/` | CLI entry point, wiring only |
| `internal/config/` | configuration types, parsing, validation and tests |
| `internal/pipeline/` | media plane boundary + ffmpeg process backend |
| `internal/relay/` | control plane: supervision, restart and backoff |
| `nix/package.nix` | Go package build, ffmpeg wrapped onto `PATH` |
| `examples/icecast.xml` | development Icecast configuration |
| `scripts/e2e-smoke.sh` | deterministic local HLS → audiorelay → Icecast smoke test |
| `.github/workflows/ci.yml` | GitHub Actions quality and integration checks |
| `docs/` | architecture notes |

## Development

```sh
nix develop          # Go, gopls, ffmpeg, icecast, curl, python3
go build ./...
go vet ./...
go run ./cmd/audiorelay -h

# Deterministic local end-to-end smoke test
./scripts/e2e-smoke.sh
```

The smoke test generates a local AAC/HLS source, starts a temporary Icecast
instance, runs audiorelay, captures the Ogg Opus mount, and verifies OggS,
Opus/stereo/48 kHz metadata and an expected bitrate range. It does not require
an external broadcaster. Set `AUDIORELAY_BIN` to test an existing binary, or
`AUDIORELAY_CAPTURE_SECONDS` to change the capture duration.

Nix build and run:

```sh
nix build            # ./result/bin/audiorelay
nix run              # same binary via the flake app
```

## NixOS service and credentials

The flake exports `nixosModules.default` (also available as
`nixosModules.audiorelay`). The module runs the relay as a dynamic systemd
user and uses `LoadCredential` so the Icecast source password is neither in
the Nix store nor in the service command line.

```nix
{
  inputs.audiorelay.url = "github:srcres258/audiorelay";

  outputs = { nixpkgs, audiorelay, ... }: {
    nixosConfigurations.host = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        audiorelay.nixosModules.default
        ({ ... }: {
          services.audiorelay = {
            enable = true;
            input = "https://upstream.example/live.m3u8";
            # Username only: do not put the password into this URL.
            output = "icecast://source@[::1]:8000/tv.opus";
            sourcePasswordFile = "/run/secrets/audiorelay-source-password";
          };
        })
      ];
    };
  };
}
```

`sourcePasswordFile` must be a **string** pointing to a runtime secret file
managed by sops-nix, agenix, or an equivalent tool; do not use a Nix path
literal or `builtins.toFile`, which would copy the secret into the store. The
systemd manager must be able to read the original file. At start, systemd
copies it into the service's private credential directory; audiorelay reads
that copy through `-source-password-file`.

The module only manages audiorelay's outbound connection. Configure and expose
Icecast, including its firewall port, separately. It restarts transient failures
with systemd, but does not restart permanent configuration or upstream failures
(exit status 78 / `EX_CONFIG`).

Note: `nix build` only sees files tracked by Git (or, outside a Git
repository, the whole directory tree). Track new sources before building in a
Git checkout.

## Planned milestones

1. ~~MVP: ffmpeg + Icecast relaying one upstream HLS audio track.~~
2. ~~Go control plane: flag parsing, structured logs and restart with backoff.~~
3. ~~NixOS service module (`nix/module.nix`), evaluated by `nix flake
   check`, and CI end-to-end checks.~~
4. Per-architecture verification for `riscv64-linux` and `loongarch64-linux`.
5. Upstream network decoupling: Go-side playlist and segment fetching with
   retry and a rolling segment cache, replayed to ffmpeg from a local
   endpoint, so a brief upstream outage no longer ends the pipeline.
6. Listener continuity across pipeline restarts (client reconnect or silence
   fill), plus health/metrics visibility.
7. Optional: in-process media backend, native HTTP broadcast sink, multi-channel.

## License

BSD 3-Clause License. See [LICENSE.txt](LICENSE.txt).
