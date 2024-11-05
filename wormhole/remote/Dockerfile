# build wormhole rootfs server image (taken from rootfs/Dockerfile)
# Build Rust - vinit, wormhole-attach, dctl
# workaround for Rosetta bug: use Rust image to avoid patchelf
FROM alpine:20240807 AS base
FROM rust:1.80-alpine3.20 AS rust-base
FROM nixos/nix:2.24.2 AS nix-base

# build wormhole-attach and dctl
FROM rust-base AS rust-build
RUN apk add --no-cache musl-dev protoc
COPY vinit /build/vinit
ARG ARCH=arm64
ARG TYPE=debug
# wormhole
COPY wormhole /build/wormhole
RUN --mount=type=cache,target=/root/.cargo/registry \
    --mount=type=cache,target=/build/wormhole/target,id=${ARCH} \
    --mount=type=ssh,required=true \
    <<EOT sh -eux
cd /build/wormhole
if [[ "$TYPE" == "debug" ]]; then
    cargo build
else
    cargo build --release
fi
mkdir /out
cp target/${TYPE}/dctl target/${TYPE}/wormhole-attach target/${TYPE}/client target/${TYPE}/server /out/
EOT

# build wormhole-rootfs
FROM nix-base AS nix-build
RUN <<EOT sh -eux
# disable seccomp to fix rosetta build
echo 'filter-syscalls = false' >> /etc/nix/nix.conf
# enable flakes
echo 'experimental-features = nix-command flakes' >> /etc/nix/nix.conf
nix-channel --update
# for zsh cloning and CNF processing
nix-env -iA nixpkgs.git nixpkgs.sqlite nixpkgs.gnutar nixpkgs.curl nixpkgs.xz nixpkgs.python3
EOT
COPY wormhole/os /build/os
COPY wormhole/cnf-gpt /build/cnf-gpt
RUN --mount=type=cache,target=/nix-cache \
    <<EOT sh -eux
mkdir /build-result
cd /build-result
# TODO: cache with --store /nix-cache --eval-store local
nix build /build/os
tar xzf result

mkdir /out
cd /out
tar xf /build-result/*/layer.tar

mkdir -p nix/orb/sys
mv bin etc share nix/orb/sys/
mkdir nix/orb/sys/.bin
# hide nix tools
mv nix/orb/sys/bin/nix* nix/orb/sys/.bin/

# no need to create gcroots - we use custom gc path filtering in rust

# we don't need this to be valid nix anymore (due to no nix.db)
# so delete stuff we don't need:
# locales
find nix/store -name '*.mo' -delete
# python3 (75M) - brought in by vim
rm -fr nix/store/*-python3-*
# nix store has no write perm for owner by default
# need this to make it work for containers without CAP_DAC_OVERRIDE, even with uid=0 gid=0
chmod 755 nix/store

# always use latest DB, and then merge overrides
/build/cnf-gpt/download-db.sh > /build/cnf.csv
/build/cnf-gpt/merge-overrides.py /build/cnf.csv /build/cnf-gpt/overrides.csv nix/orb/sys/.programs.csv

mkdir -p nix/orb/sys/zsh nix/orb/sys/.base nix/orb/data/home nix/orb/data/tmp nix/orb/data/env nix/var
cd nix/orb/sys/zsh
bash /build/os/zsh/clone.sh
cp /build/os/zsh/.zshrc .zshrc

# clean up PATH
ln -s /nix/orb/sys/zsh/cache/https-COLON--SLASH--SLASH-github.com-SLASH-bigH-SLASH-git-fuzzy/bin/git-fuzzy /out/nix/orb/sys/bin/

# terminfo for ghostty (nix installs kitty and contour)
mkdir -p /out/nix/orb/sys/share/terminfo/{x,g}
cp /build/os/terminfo/xterm-ghostty /out/nix/orb/sys/share/terminfo/x/
cp /build/os/terminfo/ghostty /out/nix/orb/sys/share/terminfo/g/
EOT
COPY wormhole/os/nix.conf /out/nix/orb/sys/etc/nix.conf


# add 'o' command
COPY --from=rust-build /out/dctl /out/nix/orb/sys/bin/

FROM base AS rootfs
COPY --from=nix-build /out /wormhole-rootfs
COPY --from=rust-build /out/wormhole-attach /wormhole-attach
COPY --from=rust-build /out/client /wormhole-client
COPY --from=rust-build /out/server /wormhole-server

ENTRYPOINT ["./wormhole-server"]