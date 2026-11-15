{
  lib,
  buildGoModule,
  makeWrapper,
  ffmpeg,
}:

buildGoModule {
  pname = "audiorelay";
  version = "0.1.0";

  # The flake source is the repository root; package.nix lives one level down.
  src = lib.cleanSource ../.;

  subPackages = [ "cmd/audiorelay" ];

  # go.mod currently has no third-party dependencies, so no vendor hash is
  # needed. Replace with `vendorHash = lib.fakeHash;` once deps are added and
  # let Nix report the real value.
  vendorHash = null;

  # The Go code does not link against libav/libopus yet, so cgo stays off.
  env.CGO_ENABLED = "0";

  nativeBuildInputs = [ makeWrapper ];

  # ffmpeg is executed at runtime, not linked. Put it on the wrapper's PATH so
  # the installed package works without a manually managed ffmpeg.
  postInstall = ''
    wrapProgram $out/bin/audiorelay \
      --prefix PATH : ${lib.makeBinPath [ ffmpeg ]}
  '';

  meta = {
    description = "Lightweight live audio relay: upstream HLS to Ogg Opus";
    homepage = "https://github.com/srcres258/audiorelay";
    license = lib.licenses.mit;
    mainProgram = "audiorelay";
    platforms = lib.platforms.linux;
  };
}
