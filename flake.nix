{
  description = "audiorelay - lightweight live audio relay";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      # Only architectures verified to build the full media stack are listed.
      # Go itself supports more targets, but ffmpeg/icecast availability in
      # nixpkgs still has to be checked per platform (see docs/architecture.md).
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];

      forAllSystems =
        f:
        nixpkgs.lib.genAttrs systems (
          system:
          f (
            import nixpkgs {
              inherit system;
            }
          )
        );
    in
    {
      packages = forAllSystems (pkgs: {
        default = pkgs.callPackage ./nix/package.nix { };
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            delve
            ffmpeg
            icecast
            curl
            pkg-config
          ];

          shellHook = ''
            echo "audiorelay dev environment"
            go version
            echo "ffmpeg: $(ffmpeg -version | head -n1)"
          '';
        };
      });

      apps = forAllSystems (pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.default}/bin/audiorelay";
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt-rfc-style);
    };
}
