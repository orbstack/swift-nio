{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }: let
    supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
    forEachSupportedSystem = f: nixpkgs.lib.genAttrs supportedSystems (system: f {
      pkgs = import nixpkgs { inherit system; };
      system = system;
    });
  in {
    packages = forEachSupportedSystem ({ pkgs, system }: {
      default = pkgs.dockerTools.buildImage {
        name = "ghcr.io/orbstack/wormhole-os";

        copyToRoot = pkgs.buildEnv {
          name = "image-root";
          paths = with pkgs; [
              zsh
              bash # users expect this - but no fish
              coreutils
              findutils
              diffutils
              gnused
              gzip
              gawk
              gnugrep
              less
              kitty.terminfo
              procps
              util-linux
              gnutar
              # network
              curl
              cacert
              iproute2
              iputils
              dig.host
              # dev
              jq
              vim
              nano
              fd
              ripgrep
              lsd
              # system
              htop
              lsof
              strace
              # stable nix is 2.18 - we want profile pkg names
              nixVersions.nix_2_20

              # nice to have but too many deps:
              #neovim
              git
          ];
          pathsToLink = [ "/bin" "/etc/ssl/certs" "/share/terminfo" "/share/man" ];
        };
      };
    });
  };
}
