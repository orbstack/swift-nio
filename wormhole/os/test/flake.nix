{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs }: let
    supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
    forEachSupportedSystem = f: nixpkgs.lib.genAttrs supportedSystems (system: f {
      pkgs = import nixpkgs { inherit system; };
      system = system;
    });
  in {
    packages = forEachSupportedSystem ({ pkgs, system }: {
      default = pkgs.buildEnv {
          name = "wormhole-env";
          paths = with pkgs; [
            go
            gotools
            golangci-lint
          ];
          pathsToLink = [ "/bin" "/etc/ssl/certs" "/share/terminfo" "/share/man" ];
        };
    });
  };
}
