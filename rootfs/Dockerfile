# syntax=docker/dockerfile:1

# Base image versions
ARG HOST_ARCH=amd64
FROM alpine:20230329 AS base
FROM alpine:3.18 AS stable-base
FROM debian:sid-slim AS debian
FROM rust:1.70-alpine3.18 AS rust-base
FROM nixos/nix:2.20.3 AS nix-base



##########################################################################
# Build Go and eBPF (C) - scon + eBPF, macctl
FROM base AS go-base
RUN apk add --no-cache go alpine-sdk lxc-dev bash \
    llvm clang linux-headers libbpf-dev
# must build vmgr and scon together because they depend on each other
FROM go-base AS go-build
COPY vendor /build/vendor
COPY scon /build/scon
COPY vmgr /build/vmgr
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg \
<<EOT sh -eux
cd /build/vmgr
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/macctl ./cmd/macctl
strip /out/macctl
EOT
ARG TYPE=release
# eBPF
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg \
<<EOT sh -eux
cd /build/scon
make bpfgen
EOT
# Go
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg \
<<EOT sh -eux
cd /build/scon
mkdir -p /out && ./build-release.sh /out ${TYPE}

# btfstrip for wormholefs build
go build -o /out/btfstrip ./cmd/btfstrip
EOT

# Build Rust - vinit, wormholefsd
# workaround for Rosetta bug: use Rust image to avoid patchelf
FROM rust-base AS rust-build
# dep needed with rust image: to build wormholefs bpf
RUN apk add --no-cache musl-dev \
    llvm clang linux-headers libbpf-dev
COPY vinit /build/vinit
ARG ARCH=arm64
ARG TYPE=release
# Rust can't deal with arch change in target/
RUN --mount=type=cache,target=/root/.cargo/registry \
    --mount=type=cache,target=/build/vinit/target,id=${ARCH} \
<<EOT sh -eux
cd /build/vinit
if [[ "$TYPE" == "debug" ]]; then
    cargo build
else
    cargo build --release
fi
mkdir /out
cp target/${TYPE}/vinit target/${TYPE}/b3enc target/${TYPE}/simplevisor /out/
EOT
# wormholefsd
COPY --from=go-build /out/btfstrip /bin/btfstrip
COPY wormhole/wormholefs /build/wormholefs
RUN --mount=type=cache,target=/root/.cargo/registry \
    --mount=type=cache,target=/build/wormholefs/target,id=${ARCH} \
    --mount=type=ssh,required=true \
<<EOT sh -eux
cd /build/wormholefs
if [[ "$TYPE" == "debug" ]]; then
    cargo build
else
    cargo build --release
fi
cp target/${TYPE}/wormholefsd /out/
EOT

# Build Rosetta runc override
FROM go-base AS runc-build
RUN apk add --no-cache libseccomp-dev libseccomp-static
COPY rootfs/pkgs/runc.patch /build/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg \
<<EOT sh -eux
mkdir /out
cd /build
git clone --depth 1 -b v1.1.12 https://github.com/opencontainers/runc
cd runc
patch -p1 < ../runc.patch
EXTRA_LDFLAGS="-s -w" make static
strip runc
mv runc /out/
EOT

# Build C - vsocknfs, rvfs-wrapper, fuse passthrough "fpll"
FROM base AS c-base
RUN apk add --no-cache alpine-sdk linux-headers fuse3-dev
FROM c-base AS c-build
COPY rootfs/utils/*.c /build/
COPY rootfs/utils/fuse /build/fuse
COPY --from=runc-build /out/runc /build/
RUN <<EOT sh -eux
mkdir /out /out-guest
g++ -O2 -Wall /build/fuse/fpll.cpp $(pkg-config fuse3 --cflags --libs) -o /out/fpll
EOT
RUN <<EOT sh -eux
gcc -O2 -Wall -o /out/vsocknfs /build/vsocknfs.c
gcc -O2 -Wall -static -o /out/rstub /build/rvfs-wrapper.c
gcc -O2 -Wall -static -o /out-guest/pstub /build/pstub.c
strip /out/* /out-guest/*

# append runc.arm64 to rvfs-wrapper
cat /build/runc >> /out/rstub
EOT

# Build wormhole's debugos rootfs
FROM nix-base as nix-build
RUN nix-channel --update
COPY wormhole/os /build/os
RUN <<EOT sh -eux
mkdir /build-result
cd /build-result
nix-build /build/os/docker.nix
tar xzf result

mkdir /out
cd /out
tar xf /build-result/*/layer.tar
EOT

# Generate Rosetta deltas (need Debian for bsdiff)
FROM debian as rosetta-deltas
RUN apt-get update && \
    apt-get install -y curl python3.10 bsdiff b3sum 7zip
COPY rootfs/utils/rosetta /work/
WORKDIR /work
COPY --from=rust-build /out/b3enc /work/
RUN --mount=type=cache,target=/work/cache \
    ./generate-deltas.sh

##########################################################################
# Docker container rootfs
# ref: https://github.com/docker-library/docker/blob/master/20.10/dind/Dockerfile
FROM base AS docker

# removed: xfsprogs e2fsprogs btrfs-progs
RUN apk add --no-cache \
    ip6tables \
    iptables \
    openssl \
    shadow-uidmap \
    xz \
    pigz \
    # Docker engine deps
    ca-certificates \
    # important to pin version for security
    runc=1.1.12-r0 \
    containerd \
    # support Docker build from git repos
    git \
    openssh-client-default \
    # for migration xattrs
    tar \
    # /etc/localtime for containers
    tzdata \
    # for kubectl port-forward
    socat
    # for tls proxy
    #ipset

# workaround: latest ipset edge 7.20-r0 segfaults
RUN <<EOT sh -eux
if [[ "\$(uname -m)" == "x86_64" ]]; then
    wget https://dl-cdn.alpinelinux.org/alpine/v3.19/main/x86_64/ipset-7.19-r2.apk
    apk add --no-cache --no-network ./ipset-7.19-r2.apk
    rm -f ipset-7.19-r2.apk
else
    wget https://dl-cdn.alpinelinux.org/alpine/v3.19/main/aarch64/ipset-7.19-r2.apk
    apk add --no-cache --no-network ./ipset-7.19-r2.apk
    rm -f ipset-7.19-r2.apk
fi
EOT

# Official Docker engine build
RUN <<EOT sh -eux
mkdir /work
cd /work
wget -O docker.tar.gz "https://download.docker.com/linux/static/stable/\$(uname -m)/docker-25.0.3.tgz"
tar -xvf docker.tar.gz
mv docker/dockerd docker/docker-init /usr/local/bin/

cd /
rm -rf /work
EOT

# k3s
ARG ARCH=arm64
COPY rootfs/k8s/k3s-${ARCH} /usr/local/bin/k3s
RUN <<EOT sh -eux
chmod +x /usr/local/bin/k3s

# we have these commands, might as well expose them
ln -s /usr/local/bin/k3s /usr/local/bin/kubectl
ln -s /usr/local/bin/k3s /usr/local/bin/crictl
ln -s /usr/local/bin/k3s /usr/local/bin/ctr
EOT

# network
COPY \
    rootfs/config/resolv.conf \
    rootfs/config/os-release \
    rootfs/config/docker/hosts \
    # k8s wants machine-id
    rootfs/config/docker/machine-id \
    /etc/
RUN <<EOT sh -eux
# vanity name
echo orbstack > /etc/hostname
# for daemon.json generation
mkdir -p /etc/docker

# set up subuid/subgid so that "--userns-remap=default" works out-of-the-box
addgroup -S dockremap
adduser -S -G dockremap dockremap
echo 'dockremap:165536:65536' >> /etc/subuid
echo 'dockremap:165536:65536' >> /etc/subgid

# mounts
mkdir /mnt/mac
mkdir /opt/orbstack-guest
mkdir /var/lib/docker
rm -rf /tmp
ln -sf /private/tmp /tmp
ln -sf /mnt/mac/var/folders /var/
mkdir /dockertmp
EOT

# sets up ipv6 nat
COPY rootfs/utils/docker-init.sh /opt/init



##########################################################################
# Patched qemu for arm64
# newer qemu 7.2 breaks openssl rsa verification
FROM stable-base as pkgbuild-qemu

# deps for build, qemu
RUN apk add --no-cache \
    alpine-sdk git \
    meson bash alsa-lib-dev bison capstone-dev curl-dev flex glib-dev glib-static gnutls-dev gtk+3.0-dev libaio-dev libcap-dev libcap-ng-dev libjpeg-turbo-dev libnfs-dev libpng-dev libseccomp-dev libslirp-dev libssh-dev liburing-dev libusb-dev libxml2-dev linux-headers lzo-dev ncurses-dev numactl-dev perl pulseaudio-dev python3 py3-sphinx py3-sphinx_rtd_theme sdl2-dev snappy-dev spice-dev texinfo usbredir-dev util-linux-dev vde2-dev virglrenderer-dev vte3-dev xfsprogs-dev zlib-dev zlib-static zstd-dev
RUN <<EOT sh -eux
mkdir /work
cd /work
curl -LO https://wiki.qemu-project.org/download/qemu-7.1.0.tar.xz
echo "c60c5ff8ec99b7552e485768908920658fdd8035ff7a6fa370fb6881957dc8b7e5f18ff1a8f49bd6aa22909ede2a7c084986d8244f12074ccd33ebe40a0c411f  qemu-7.1.0.tar.xz" > checksum
sha512sum -c checksum

tar xf qemu-7.1.0.tar.xz
mv qemu-7.1.0 qemu

mkdir patches
EOT

COPY rootfs/pkgs/qemu/*.patch /work/patches/
RUN <<EOT sh -eux
cd /work/qemu
for p in /work/patches/*.patch; do
    patch -p1 < \$p
done
EOT

# TODO fix cache
RUN --mount=type=cache,target=/work/qemu/build2 \
<<EOT sh -eux
cd /work/qemu
export CFLAGS="-O2 -flto=auto"
./configure \
    --disable-glusterfs \
    --disable-debug-info \
    --disable-bsd-user \
    --disable-werror \
    --disable-xen \
    --enable-linux-user \
    --disable-system \
    --static \
    --disable-brlapi \
    --disable-cap-ng \
    --disable-capstone \
    --disable-curl \
    --disable-curses \
    --disable-docs \
    --disable-gcrypt \
    --disable-gnutls \
    --disable-gtk \
    --disable-guest-agent \
    --disable-guest-agent-msi \
    --disable-libnfs \
    --disable-mpath \
    --disable-nettle \
    --disable-numa \
    --disable-sdl \
    --disable-spice \
    --disable-tools
EOT

RUN --mount=type=cache,target=/work/qemu/build2 \
<<EOT sh -eux
if [[ "\$(uname -m)" != "aarch64" ]]; then
    echo "skip"
    mkdir /out
    touch /out/qemu-x86_64 /out/qemu-i386
    exit
fi

cd /work/qemu
export CFLAGS="-O2 -flto=auto"
make -j\$(nproc) ARFLAGS="rc" qemu-x86_64
make -j\$(nproc) ARFLAGS="rc" qemu-i386
make -j\$(nproc) ARFLAGS="rc" qemu-arm

mkdir /out
cp /work/qemu/build/qemu-x86_64 /work/qemu/build/qemu-i386 /work/qemu/build/qemu-arm /out/
strip /out/*
EOT



##########################################################################
# Main rootfs!
FROM base AS rootfs

# Dependencies
RUN apk add --no-cache \
    # basic
    chrony eudev \
    # disk
    sfdisk nfs-utils \
    # scon deps
    lxc-libs tar squashfs-tools ca-certificates dnsmasq iptables ip6tables xz btrfs-progs \
    # modprobe metadata
    kmod \
    # qemu for other archs, common between x86 and arm.
    # latest is probably best, esp. for riscv which is less mature
    # also saves build time on arm64
    # no one uses 32-bit ppc for docker (28 images total on docker hub)
    qemu-riscv64 qemu-ppc64le qemu-s390x qemu-mips64el \
    # for nfs containers
    fuse3-libs libstdc++

# debug deps
ARG TYPE=release
RUN <<EOT sh -eux
if [[ "$TYPE" == "debug" ]]; then
    apk add --no-cache neovim iperf3 iproute2 agetty openssh tmux htop strace curl evtest powertop sysstat quota-tools util-linux tcpdump ethtool mtr bind btrfs-progs-extra f2fs-tools fish go git lxc-dev alpine-sdk socat llvm clang linux-headers libbpf-dev bpftool
fi
EOT

# extract needed nfs utils, discard the rest to reduce size (python)
# keep required libs
RUN <<EOT sh -eux
mkdir /opt/pkg
cd /opt/pkg

cp $(command -v exportfs) $(command -v rpc.nfsd) $(command -v rpc.mountd) /opt/pkg/

apk del nfs-utils
apk add --no-cache libtirpc
EOT

# qemu for arm64 host
COPY --from=pkgbuild-qemu /out/* /opt/orb/

# qemu for x86_64 host
# 7.2 breaks openssl rsa verification (for x86_64 on arm64)
# 8.0 segfaults in rustc/cargo like rosetta vmtracker
RUN <<EOT sh -eux
if [[ "\$(uname -m)" == "x86_64" ]]; then
    wget https://dl-cdn.alpinelinux.org/alpine/v3.17/community/x86_64/qemu-aarch64-7.1.0-r7.apk
    wget https://dl-cdn.alpinelinux.org/alpine/v3.17/community/x86_64/qemu-arm-7.1.0-r7.apk
    apk add --no-cache --no-network ./qemu-aarch64-7.1.0-r7.apk ./qemu-arm-7.1.0-r7.apk
    rm -f qemu-aarch64-7.1.0-r7.apk qemu-arm-7.1.0-r7.apk

    ln -s /usr/bin/qemu-arm /opt/orb/qemu-arm
fi
EOT

# Docker container rootfs
COPY --from=docker / /opt/docker-rootfs

# configs
COPY \
    rootfs/config/hosts \
    rootfs/config/resolv.conf \
    rootfs/config/os-release \
    /etc/
COPY rootfs/config/ssh_host_keys /etc/ssh
# NTP
COPY rootfs/config/chrony.conf /etc/chrony/chrony.conf
# kernel modules
ARG ARCH=arm64
# 0.0.0 = temp name for depmod
COPY rootfs/kernel/${ARCH} /opt/orbstack-guest/lib/modules/0.0.0

# debug services
COPY rootfs/config/authorized_keys /root/.ssh/authorized_keys
RUN <<EOT sh -eux
# kernel module info for modprobe
depmod -b /opt/orbstack-guest 0.0.0
mv /opt/orbstack-guest/lib/modules/0.0.0 /opt/orbstack-guest/lib/modules/current

if [[ "$TYPE" == "debug" ]]; then
    chown 0600 /etc/ssh/*key*
    sed -i 's|/bin/ash|/usr/bin/fish|' /etc/passwd
    echo 'hvc0::respawn:/sbin/agetty -L hvc0 115200 vt100 --autologin root' >> /etc/inittab
else
    # release
    # remove ssh host keys
    rm -fr /etc/ssh
    rm -fr /root/.ssh
    # uninstall apk
    apk del apk-tools kmod
fi
EOT

# misc setup
RUN <<EOT sh -eux
# remove motd
rm -f /etc/motd

# LXC
# liblxc requires this to start
mkdir -p /usr/lib/lxc/rootfs

# Mountpoints
mkdir /nfs
mkdir /data
mkdir /mnt/mac
mkdir /mnt/rosetta
mkdir /mnt/rv

# binfmt vanity links
ln -s /tmp/rstub '[rosetta]' # (real rosetta is in rvfs)
ln -s /opt/orb/qemu-x86_64 '[qemu]'
ln -s /opt/orb/qemu-i386 '[qemu32]'
ln -s /opt/orb/qemu-arm '[qemu-arm32]'
ln -s /usr/bin/qemu-aarch64 '[qemu-arm64]'
ln -s /usr/bin/qemu-riscv64 '[qemu-riscv64]'
ln -s /usr/bin/qemu-ppc64le '[qemu-ppc64le]'
ln -s /usr/bin/qemu-s390x '[qemu-s390x]'
ln -s /usr/bin/qemu-mips64el '[qemu-mips64el]'
ln -s /opt/orbstack-guest/bin/macctl '[mac]'

# Guest tools
mkdir -p /opt/orbstack-guest/bin /opt/orbstack-guest/bin-hiprio /opt/orbstack-guest/run /opt/orbstack-guest/data
ln -s /opt/orbstack-guest/bin/macctl /opt/orbstack-guest/bin/mac
# default cmd links
for cmd in open; do
    ln -s /opt/orbstack-guest/bin/macctl /opt/orbstack-guest/bin-hiprio/\$cmd
done
# control from mac
for cmd in caffeinate code mdfind mdls open osascript pbcopy pbpaste pmset qlmanage screencapture softwareupdate system_profiler orb orbctl; do
    ln -s /opt/orbstack-guest/bin/macctl /opt/orbstack-guest/bin/\$cmd
done

# Listeners for ssh-agent socket
# doesn't need to be created on every boot, and this avoids showing up in mount (because same fs as /opt/orbstack-guest)
mkdir -p /opt/orb/launchd-ssh-agent-listeners
ln -s /opt/orbstack-guest/run/host-ssh-agent.sock /opt/orb/launchd-ssh-agent-listeners/Listeners

# NFS state (need to create because we removed nfs-utils package)
mkdir -p /var/lib/nfs /var/lib/nfs/v4recovery
touch /var/lib/nfs/etab /var/lib/nfs/rmtab /var/lib/nfs/state
EOT

# Scripts
COPY rootfs/utils/orb /opt/orb
COPY rootfs/utils/guest/etc /opt/orbstack-guest/etc
COPY LICENSE /

# Rosetta deltas
COPY --from=rosetta-deltas /out/ /opt/orb/delta

# Compiled binaries (in order of most likely to change)
COPY --from=c-build /out/* /opt/orb/
COPY --from=c-build /out-guest/* /opt/orbstack-guest/
# vinit
COPY --from=rust-build /out/vinit /opt/orb/
COPY --from=rust-build /out/simplevisor /opt/orbstack-guest/
# wormhole
COPY --from=rust-build /out/wormholefsd /opt/orbstack-guest/
COPY --from=nix-build /out /opt/wormhole-rootfs
# macctl
COPY --from=go-build /out/macctl /opt/orbstack-guest/bin/
# scon
COPY --from=go-build /out/scon /out/scon-agent /opt/orb/
COPY --from=go-build /out/scon-forksftp /opt/orbstack-guest/

LABEL type=$TYPE



# Final images
FROM --platform=${HOST_ARCH} debian AS packer
RUN apt-get update && \
    apt-get install -y btrfs-progs erofs-utils libarchive-tools qemu-utils fdisk
RUN mkdir /images

# rootfs
FROM packer AS pack-images
COPY --from=rootfs / /rootfs
ARG TYPE=release
RUN <<EOT bash -eux
comp_alg=lz4hc
if [[ "$TYPE" == "debug" ]]; then
    comp_alg=lz4
fi
# -Ededupe is too slow
mkfs.erofs -Efragments -Eztailpacking --ignore-mtime /images/rootfs.img /rootfs -z \$comp_alg
EOT

# speed up exporting images by skipping /rootfs
FROM packer as images
COPY --from=pack-images /images /images
