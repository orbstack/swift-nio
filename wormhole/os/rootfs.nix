# Build with:
#     NIX_PATH=nixpkgs=$HOME/src/nixpkgs nix-build --no-link '<nixpkgs/nixos>' -A config.system.build.tarball -I nixos-config=thisfile.nix
# You can also use
#     -A config.system.build.toplevel
# to build something you can browse locally (that uses symlinks into your nix store).

{config, pkgs, ...}:
{
  # We need no bootloader, because the Chromebook can't use that anyway.
  boot.loader.grub.enable = false;

  fileSystems = {
    # Mounts whatever device has the NIXOS_ROOT label on it as /
    # (but it's only really there to make systemd happy, so it wont try to remount stuff).
    "/".label = "NIXOS_ROOT";
  };

  # Trim locales a lot to save disk space (but sacrifice translations).
  # Unfortunately currently only gets rid of the large `glibc-locales`
  # package (120 MB as of writing);
  # the individual packages still have all their big `.mo` files.
  i18n.supportedLocales = [ (config.i18n.defaultLocale + "/UTF-8") ];

  # packages
  environment.systemPackages = with pkgs; [
    # The basic system
    bash
    coreutils
    findutils
    gnused
    gzip
    gawk
    grep
    less
    procps
    tar
    which
    # Networking
    curl
    iproute
    iputils
    openssh
    wget
    # Development
    git
    # System
    lsof
    strace
    # Other
  ];

  system.build.tarball = pkgs.callPackage <nixpkgs/nixos/lib/make-system-tarball.nix> {
    contents = [];
    compressCommand = "cat";
    compressionExtension = "";
  };

  # Empty root password
  users.users.root.password = "";
}
