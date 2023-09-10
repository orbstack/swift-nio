# OrbStack

## Developer onboarding

- Install and start a release build of OrbStack
- Set `GOPRIVATE` and ssh->git redirect
- Build rootfs: `cd rootfs; make`
- Build CLI: `cd scon; make`
- Build kernel from https://github.com/orbstack/linux-macvirt-priv
  - From another machine: `make sync`
  - `source .in && mall`
- Attempt a release build: `./build.sh`
  - Includes rootfs, vmgr, scli
  - It will fail — that's okay.
- Generate Apple Development certificate
- Register device with Apple Developer
- Generate and install Apple Development provisioning profile
- Build and run app in Xcode

## Licenses generation

To install tools for generating licenses:

```bash
go install github.com/google/go-licenses@latest
cargo install --locked cargo-about
```

Then run `scripts/gen-licenses.sh`.
