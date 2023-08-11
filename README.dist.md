# OrbStack builds

Prerequisites:

- macOS 13 machine
- Linux machine

## Team ID setup

- Find your 10-character Apple Developer Team ID
- Find and replace `HUAQ24HBR6` with your team ID in all files

## Entitlement setup

- Go to Apple Developer -> Identifiers: https://developer.apple.com/account/resources/identifiers/list
- Create an App ID with an explicit bundle ID of "com.anthropic.OrbStackInternal". Description doesn't matter.
  - Enable "VM Networking" in the Additional Capabilities tab
- Go to Certificates and create a Developer ID Application certificate, or create it from Xcode, or use an existing (e.g. enterprise) certificate.
- Go to Profiles and create a Developer ID provisioning profile. Select the App ID and certificate created above.
- Download this profile and save it at `vmgr/bundle/embedded.provisionprofile`. Also import it into Xcode (open `swift/MacVirt.xcodeproj` and import it in Signing & Capabilities -> Provisioning Profile. Not sure how to do this from the command line.)
- Change the Team under "Signing (Release)" for each target in Xcode.

## Notarization setup

- Run `xcrun notarytool store-credentials` and log in to the Apple notary service.

## Miscellaneous config

Edit `config.sh` as necessary.

## Kernel build

This build is done on an Arch Linux install (x86_64 host) with the following packages:

```bash
pacman -Syu aarch64-linux-gnu-binutils aarch64-linux-gnu-gcc aarch64-linux-gnu-glibc
```

Correct results cannot be guaranteed in any other environments due to toolchain and compiler differences.

- Run `./build.sh` in `vendor/linux`
- Copy the following files to the macOS host, creating directories as necesasry:
  - `vendor/linux/out/arch/arm64/boot/Image` -> `vmgr/assets/release/arm64/kernel`
  - `vendor/linux/out/modules.builtin` -> `rootfs/kernel/arm64/modules.builtin`
  - `vendor/linux/out86/arch/x86/boot/bzImage` -> `vmgr/assets/release/x86/kernel`
  - `vendor/linux/out86/modules.builtin` -> `rootfs/kernel/x86/modules.builtin`

## App build

- Install and start a Docker provider on the macOS host. This has only been tested with OrbStack, but a remote Linux builder might also work (untested).
- Run `make` at the repo root

Final DMGs ready for use can be found at `swift/out`.

## Publish update feed

TBD

## Potential issues

If the GUI app fails to launch, or any component crashes with `SIGKILL`, you have a code signing issue. Check `sudo log stream` to see why.
