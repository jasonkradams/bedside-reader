{
  description = "Decrypt Audible .aax audiobooks into DRM-free .m4b with a minimal, source-built ffmpeg";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    nixos-hardware.url = "github:NixOS/nixos-hardware/master";
    nixos-generators = {
      url = "github:nix-community/nixos-generators";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    { self, nixpkgs, nixos-hardware, ... }:
    let
      systems = [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAllSystems =
        f: nixpkgs.lib.genAttrs systems (system: f system nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (
        _system: pkgs: pkgs.callPackage ./nix/packages.nix {}
      );

      apps = forAllSystems (
        system: _pkgs: {
          default = {
            type = "app";
            program = "${self.packages.${system}.audible-convert}/bin/audible-convert";
          };

          build-os = {
            type = "app";
            program = "${self.packages.${system}.build-os}/bin/build-os";
          };
        }
      );

      devShells = forAllSystems (
        system: pkgs: {
          default = pkgs.mkShell {
            packages = [
              self.packages.${system}.ffmpeg

              self.packages.${system}.build-os
              self.packages.${system}.flash-os
              self.packages.${system}.go_1_26_4
              pkgs.colmena
            ];
          };
        }
      );

      colmena = {
        meta = {
          specialArgs = {
            inherit self;
            bedside-app = self.packages.aarch64-linux.bedside-app;
          };
        };

        bedside-pi = { ... }: {
          nixpkgs.hostPlatform = "aarch64-linux";
          deployment = {
            targetHost = "10.136.117.83"; # Default IP, user can override in ~/.ssh/config or modify here
            targetUser = "root";
            # Use cross-compilation or Docker if the deployment is initiated from macOS
            buildOnTarget = false;
          };
          
          imports = [
            nixos-hardware.nixosModules.raspberry-pi-3
            ./system/configuration.nix
          ];
        };
      };

      nixosConfigurations = {
        bedside-pi = nixpkgs.lib.nixosSystem {
          specialArgs = {
            inherit self;
            bedside-app = self.packages.aarch64-linux.bedside-app;
          };
          modules = [
            { nixpkgs.hostPlatform = "aarch64-linux"; }
            "${nixpkgs}/nixos/modules/installer/sd-card/sd-image-aarch64.nix"
            nixos-hardware.nixosModules.raspberry-pi-3
            ./system/configuration.nix
            ./system/sd-image-opts.nix
          ];
        };
      };

    };
}
