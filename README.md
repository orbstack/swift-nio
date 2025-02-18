# OrbStack

## Components

- `swift/MacVirt`: macOS GUI app (Swift)
- `vmgr`: Runs VM and provides integration services (Go)
  - Includes `swift/GoVZF`: Virtualization.framework bindings and other native Swift
- `rootfs`: Linux distro that runs in the VM (mixed: C, C++, Alpine Linux)
  - Includes `vinit` and `scon`
- `vinit`: PID 1 init process and service manager. Runs in the VM. (Rust)
  - Starts `scon`
- `scon`: Container manager that runs containers ("machines") in the VM. (Go)
  - Includes BPF programs (C)
- `wormhole`: Debug Shell
- `virtue`: VMM for macOS (Rust)

## Developer onboarding

**If you're not working on Kubernetes or the Linux kernel, check Slack for prebuilt binaries to speed this up.**

1. `brew install rustup go protobuf protoc-gen-go && rustup-init`
1. Install and start a [release build of OrbStack](https://orbstack.dev/download)
1. Add `export GOPRIVATE=github.com/orbstack/*` to global shell environment (`~/.profile`)
1. Install GitHub CLI: `brew install gh`
    1. Sign in: `gh auth login`
    1. Configure Git for HTTPS: `gh auth setup-git`
1. Install Xcode
1. Install Xcode Command Line Tools: `xcode-select --install`
1. Set up code signing
    1. Copy "Provisioning UDID" from System Information
        - Hold Option > Apple logo in menu bar > System Information
    1. Ping Danny to add you to the Apple Developer team. Include your UDID.
    1. Xcode Settings > Accounts > sign in with Apple ID
    1. Select the "Orbital Labs, LLC (U.S.)" team > Manage Certificates
    1. Add a new Apple Development certificate
    1. Ping Danny to create a provisioning profile for you
        - Install the profile
        - **DO NOT CONTINUE** until this is done
    1. Install Worldwide Developer Relations [G3 and G6 certificates](https://www.apple.com/certificateauthority/)
1. Create `config.local.sh` in repo root with `SIGNING_CERT_DEV="..."`
    - Search for "Apple Development" in Keychain Access and copy the full certificate name
    - Example: `Apple Development: Danny Lin (A2LS84RQFY)`
1. Build Kubernetes: `./scripts/build-k8s.sh`
1. Build debug vmgr: `cd vmgr; make`
1. Build kernel
    1. Create an OrbStack machine. Only Arch and Debian have been tested.
    1. Install dependencies:
        - Debian: `sudo apt install build-essential bc cpio pahole pixz libjemalloc2 libelf-dev libssl-dev flex bison lz4 python3`
        - Arch: `sudo pacman -Syu base-devel bc cpio pahole pixz jemalloc python`
    1. `git clone git@github.com:orbstack/linux-macvirt-priv`
        - Must be on Linux file system due to case sensitivity (run `cd` — should be in /home, not /Users)
    1. `git checkout mac-6.10.x` (current dev branch)
    1. `source setup.sh`
    1. `restore_config`
    1. `mall`
    1. `sync_out $PATH_TO_MACVIRT_CLONE` (errors due to missing x86 outputs are OK)
1. Build debug rootfs: `cd rootfs; make`
1. Build debug CLI (orb command): `cd scon; make`
1. Download binaries: `cd bins; make`
1. Quit the release build of OrbStack
1. Set Xcode > Settings > Locations > Derived Data to "Relative"
1. Build and run app in Xcode
    1. Select scheme `MacVirt`
    2. Click the play button

### Onboarding for orbfs

Recommended development setup:

- VS Code + rust-analyzer for the macOS parts
- VS Code + Remote SSH into `orb` + rust-analyzer for the Linux parts

Create an OrbStack machine, either Ubuntu arm64 or Arch arm64.

Install rustup:

```bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
```

Install dependencies:

- Ubuntu: `sudo apt install build-essential clang libbpf-dev`
- Arch: `sudo pacman -Syu base-devel clang libbpf`

Make this your default OrbStack machine. Connect to `orb` in VS Code Remote SSH, install the rust-analyzer extension, and open `orbfs`.

## Development cycle

### vmgr

To build & run vmgr:

```bash
cd vmgr
make run
```

Stop with ^C.

### rootfs

To build a new rootfs:

```bash
cd rootfs
make
```

Then restart vmgr (^C then `make`) to boot it.

### scon

You can build and test this as part of rootfs, but killing and replacing the running instance of scon in the VM is faster:

```bash
cd scon
make run
```

### vinit

It's hard to replace PID 1, so just build a new rootfs and restart vmgr.

### GUI

Open Xcode and run the `MacVirt` scheme.

### Kernel

- To load helper functions: `source setup.sh`
- To restore config to committed version: `restore_config`
- To build for arm64: `marm`
- To edit config: `marm nconfig` (or `marm menuconfig` if you prefer)
- To build for x86: `mx86` (usually not needed)
- To build for both arm64 and x86: `mall` (usually not needed)
- To commit current config: `cpconfig`

After every build, copy `out/arch/arm64/boot/Image` to `assets/debug/arm64/kernel` and restart vmgr.

### orbfs

On macOS side:

```bash
cd orbfs
cargo run --bin orbfs-server
```

Linux side, in an OrbStack machine: (must run as root, so can't use `cargo run` without messing up permissions in `target`)

```bash
cd orbfs
cargo build --bin orbfs-client && sudo target/debug/orbfs-client
```

Must be running a debug build of OrbStack (vmgr) for this to work.

#### Performance testing

Debug builds have very verbose logging, so use release builds for performance testing.

macOS side:

```bash
cd orbfs
cargo run --release --bin orbfs-server
```

Linux side, in an OrbStack machine:

```bash
cd orbfs
cargo build --release --bin orbfs-client && sudo target/release/orbfs-client
```

#### Benchmarking

To run `criterion` benchmarks on the macOS side:

```bash
cd orbfs
cargo bench
```

### wormholefs

Install btfstrip in a Linux machine:

```bash
cd scon
go build -o ~/bin/btfstrip ./cmd/btfstrip
```

Build and run:

```bash
cd wormhole
cargo build && sudo target/debug/wormholefs /tmp /dev /mnt/tmp
```

### wormhole rootfs

In a NixOS machine:

```bash
cd wormhole
nix-build os/docker.nix
```

## Licenses generation

To install tools for generating licenses:

```bash
go install github.com/google/go-licenses@latest
cargo install --locked cargo-about
```

Then run `scripts/gen-licenses.sh`.

## Release builds

- `brew install pnpm`
- Get Developer ID Application certificate and key
- Import "OrbStack vmgr Developer ID Application" provisioning profile
- `notarytool store-credentials`
- Add R2 credentials to rclone
- Add Sparkle key to keychain
