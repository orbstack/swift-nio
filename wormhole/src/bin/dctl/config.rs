#[cfg(target_arch = "x86_64")]
pub const CURRENT_PLATFORM: &str = "x86_64-linux";
#[cfg(target_arch = "aarch64")]
pub const CURRENT_PLATFORM: &str = "aarch64-linux";

// TODO: better way to propagate this info from flake.nix
pub const BUILTIN_PACKAGES: &[&str] = &[
    "zsh",
    "bash",
    "coreutils",
    "findutils",
    "diffutils",
    "gnused",
    "gzip",
    "gawk",
    "gnugrep",
    "less",
    "kitty.terminfo",
    "procps",
    "util-linux",
    "gnutar",

    "curl",
    "cacert",
    "iproute2",
    "iputils",
    "dig.host",

    "jq",
    "vim",
    "nano",
    "fd",
    "ripgrep",
    "lsd",

    "htop",
    "lsof",
    "strace",
    "man",

    "nix",
    "nixVersions.nix_2_20",

    "git",
];

// packages to hide from list
pub const HIDE_BUILTIN_PACKAGES: &[&str] = &[
    "nix",
    "nixVersions.nix_2_20",
];
