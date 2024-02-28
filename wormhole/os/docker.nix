{ 
pkgs ? import <nixpkgs> { }
, pkgsLinux ? import <nixpkgs> { system = "aarch64-linux"; }, ...
}:

pkgs.dockerTools.buildImageWithNixDb {
  name = "ghcr.io/orbstack/wormhole-os";

  # Trim locales a lot to save disk space (but sacrifice translations).
  # Unfortunately currently only gets rid of the large `glibc-locales`
  # package (120 MB as of writing);
  # the individual packages still have all their big `.mo` files.
#   i18n.supportedLocales = [ (config.i18n.defaultLocale + "/UTF-8") ];

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
        # procps
        # util-linux
        gnutar # TODO: bsdtar?
        # network
        curl
        cacert
        iproute2
        iputils
        openssh
        wget
        tcpdump
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
        # self-ref!
        nix

        # nice to have but too many deps:
        #neovim
        #git
    ];
    pathsToLink = [ "/bin" ];
  };

  config = {
    Cmd = [ "/bin/zsh" ];
    Env = [
      "ZDOTDIR=/nix"
      "GIT_SSL_CAINFO=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      "NIX_SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"

      # TODO: MANPATH
      # TODO at runtime: HOSTNAME, PWD, HOME, USER, PATH
    ];
  };
}
