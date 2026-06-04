# SPDX-License-Identifier: AGPL-3.0-or-later
{
  description = "Skaldi: self-hosted network jukebox (single Go binary, embedded web UI)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      lib = nixpkgs.lib;
      allSystems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      linuxSystems = [ "x86_64-linux" "aarch64-linux" ];
      forSystems = systems: f: lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      forAllSystems = forSystems allSystems;
    in
    {
      packages = forAllSystems (pkgs: rec {
        skaldi = pkgs.callPackage ./nix/package.nix { };
        default = skaldi;
      });

      overlays.default = final: _prev: {
        skaldi = final.callPackage ./nix/package.nix { };
      };

      nixosModules.default = import ./nix/module.nix self;
      nixosModules.skaldi = self.nixosModules.default;

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            just
            mpv
            ffmpeg
            yt-dlp
            bun
            avahi
          ];
        };
      });

      checks = forSystems linuxSystems (pkgs: {
        module = pkgs.testers.runNixOSTest (import ./nix/test.nix self);
      });

      formatter = forAllSystems (pkgs: pkgs.nixpkgs-fmt);
    };
}
