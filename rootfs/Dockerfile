# syntax=docker/dockerfile:1

# Base image versions
ARG HOST_ARCH=amd64
FROM alpine:20230329 AS base
FROM debian:sid-slim AS debian



##########################################################################
# Build Go and eBPF (C) - scon + eBPF, macctl
FROM base AS go-build
RUN apk add --no-cache go alpine-sdk lxc-dev bash \
    llvm clang linux-headers libbpf-dev
COPY vendor /build/vendor
COPY scon /build/scon
COPY macvmgr /build/macvmgr
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg \
<<EOT sh -eux
cd /build/macvmgr
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/macctl ./cmd/macctl
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
EOT

# Build Rust - vcontrol
FROM base AS rust-build
RUN apk add --no-cache cargo
COPY vcontrol /build/vcontrol
ARG ARCH=arm64
# Rust can't deal with arch change in target/
RUN --mount=type=cache,target=/root/.cargo/registry \
    --mount=type=cache,target=/build/vcontrol/target,id=${ARCH} \
<<EOT sh -eux
cd /build/vcontrol
cargo build --release
mkdir /out
cp target/release/vcontrol /out/vcontrol
EOT

# Build C - switchroot, vsocknfs
FROM base AS c-build
RUN apk add --no-cache alpine-sdk linux-headers
COPY rootfs/utils/*.c /build/
RUN <<EOT sh -eux
mkdir /out /out-guest
gcc -O2 -Wall -o /out/switchroot /build/switchroot.c
gcc -O2 -Wall -o /out/vsocknfs /build/vsocknfs.c
gcc -O2 -Wall -static -o /out-guest/dummy /build/dummy.c
strip /out/* /out-guest/*
EOT



##########################################################################
# Docker container rootfs
# ref: https://github.com/docker-library/docker/blob/master/20.10/dind/Dockerfile
FROM base AS docker
# change to trigger update
RUN echo docker=v23.0.6

# removed: xfsprogs e2fsprogs btrfs-progs
RUN apk add --no-cache \
        docker-engine \
		ip6tables \
		iptables \
		openssl \
		shadow-uidmap \
		xz \
		pigz

# network
COPY \
    rootfs/config/resolv.conf \
    rootfs/config/os-release \
    /etc/
COPY rootfs/config/hosts.docker /etc/hosts
RUN <<EOT sh -eux
echo docker > /etc/hostname
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
COPY rootfs/utils/docker-entrypoint.sh /



##########################################################################
# Patched qemu for arm64
# newer qemu 7.2 breaks openssl rsa verification
FROM base as pkgbuild-qemu

# deps for build, qemu
RUN apk add --no-cache \
    alpine-sdk git \
    meson bash alsa-lib-dev bison capstone-dev curl-dev flex glib-dev glib-static gnutls-dev gtk+3.0-dev libaio-dev libcap-dev libcap-ng-dev libjpeg-turbo-dev libnfs-dev libpng-dev libseccomp-dev libslirp-dev libssh-dev liburing-dev libusb-dev libxml2-dev linux-headers lzo-dev ncurses-dev numactl-dev perl pulseaudio-dev python3 py3-sphinx py3-sphinx_rtd_theme sdl2-dev snappy-dev spice-dev texinfo usbredir-dev util-linux-dev vde2-dev virglrenderer-dev vte3-dev xfsprogs-dev zlib-dev zlib-static zstd-dev
RUN <<EOT sh -eux
if [[ "\$(uname -m)" != "aarch64" ]]; then
    echo "skip"
    exit
fi

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
if [[ "\$(uname -m)" != "aarch64" ]]; then
    echo "skip"
    exit
fi

cd /work/qemu
for p in /work/patches/*.patch; do
    patch -p1 < \$p
done
EOT

# TODO fix cache
RUN --mount=type=cache,target=/work/qemu/build2 \
<<EOT sh -eux
if [[ "\$(uname -m)" != "aarch64" ]]; then
    echo "skip"
    exit
fi

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

mkdir /out
cp /work/qemu/build/qemu-x86_64 /work/qemu/build/qemu-i386 /out/
EOT



##########################################################################
# Main rootfs!
FROM base AS rootfs

# Dependencies
RUN apk add --no-cache \
    # basic
    openrc bash dash chrony eudev \
    # disk
    # util-linux to fix "/dev/vda: Can't open blockdev" for btrfs mount
    sfdisk nfs-utils btrfs-progs util-linux \
    # scon deps
    lxc-libs tar squashfs-tools ca-certificates dnsmasq iptables ip6tables xz \
    # needed for static ip neigh
    iproute2-minimal

# debug deps
ARG TYPE=release
RUN <<EOT sh -eux
if [[ "$TYPE" == "debug" ]]; then
    apk add --no-cache neovim iperf3 iproute2 agetty openssh tmux htop strace curl evtest powertop sysstat quota-tools util-linux tcpdump ethtool mtr bind btrfs-progs-extra f2fs-tools fish go git lxc-dev alpine-sdk socat llvm clang linux-headers libbpf-dev
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
COPY --from=pkgbuild-qemu /out/qemu-x86_64 /out/qemu-i386 /opt/orb/

# qemu for x86_64 host
# 7.2 breaks openssl rsa verification (for x86_64 on arm64)
# 8.0 segfaults in rustc/cargo like rosetta vmtracker
RUN <<EOT sh -eux
if [[ "\$(uname -m)" == "x86_64" ]]; then
    wget https://dl-cdn.alpinelinux.org/alpine/v3.17/community/x86_64/qemu-aarch64-7.1.0-r7.apk
    apk add --no-cache --no-network ./qemu-aarch64-7.1.0-r7.apk
    rm -f qemu-aarch64-7.1.0-r7.apk
fi
EOT

# Docker container rootfs
COPY --from=docker / /opt/docker-rootfs

# services
RUN <<EOT sh -eux
rc-update add devfs
rc-update add sysfs
rc-update add cgroups default
rc-update add networking default
rc-update add udev default
rc-update add chronyd default
touch /etc/network/interfaces
EOT

# configs
COPY \
    rootfs/config/inittab \
    rootfs/config/fstab \
    rootfs/config/hostname \
    rootfs/config/hosts \
    rootfs/config/resolv.conf \
    rootfs/config/sysctl.conf \
    rootfs/config/rc.conf \
    rootfs/config/os-release \
    /etc/
COPY rootfs/config/ssh_host_keys /etc/ssh
# NTP
COPY rootfs/config/chrony.conf /etc/chrony/chrony.conf

# debug services
RUN <<EOT sh -eux
if [[ "$TYPE" == "debug" ]]; then
    mkdir -p /opt/orb
    touch /opt/orb/is_debug

    chown 0600 /etc/ssh/*key*
    rc-update add sshd default
    sed -i 's|/bin/ash|/usr/bin/fish|' /etc/passwd

    mkdir /root/.ssh
echo 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKE7Zy5HlH2BhRzz23wfmoO0LsSoxOfX0saf6HiL5c/c
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBWbetK7Sysq0tmjM0Hr7pwBupdEgoyDme2bcU/K30BG
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL+9Oxe4UXm5wNkkT0dx07HGFN6eqjIFzMx98oWSPCPt
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ/wCg/nWi0s+OYvjdW6JdxYaXpoO/fZvzwu0RRszPir' > /root/.ssh/authorized_keys

    echo 'hvc0::respawn:/sbin/agetty -L hvc0 115200 vt100 --autologin root' >> /etc/inittab
else
    # release
    # remove ssh host keys
    rm -fr /etc/ssh
    # uninstall apk
    apk del apk-tools
fi
EOT

# misc setup
RUN <<EOT sh -eux
# remove motd
rm -f /etc/motd

# LXC
# liblxc requires this to start
mkdir -p /usr/lib/lxc/rootfs

# NFS
# 32 threads for perf
echo 'OPTS_RPC_NFSD="32"' >> /etc/conf.d/nfs
# fix fd hang
echo 'OPTS_NFSD="nfsv4leasetime=30 nfsv4gracetime=1"' >> /etc/conf.d/nfs
mkdir /nfsroot-ro /nfsroot-rw

# speed up boot
echo 'rc_need="localmount"' >> /etc/conf.d/sshd
# dash is slightly faster
rm -f /bin/sh
ln -s /usr/bin/dash /bin/sh

# Mountpoints
mkdir /data
mkdir /mnt/mac
mkdir /mnt/rosetta
ln -s /mnt/rosetta/rosetta '/[rosetta]'
ln -s /opt/orb/qemu-x86_64 '[qemu]'
ln -s /opt/orb/qemu-i386 '[qemu32]'
ln -s /usr/bin/qemu-aarch64 '[qemu-arm64]'
ln -s /opt/orbstack-guest/bin/macctl '[mac]'

# Guest tools
mkdir -p /opt/orbstack-guest/bin /opt/orbstack-guest/bin-hiprio /opt/orbstack-guest/run /opt/orbstack-guest/data
ln -s /opt/orbstack-guest/bin/macctl /opt/orbstack-guest/bin/mac
# default cmd links
for cmd in open; do
    ln -s /opt/orbstack-guest/bin/macctl /opt/orbstack-guest/bin-hiprio/\$cmd
done
# control from mac
for cmd in osascript code orb orbctl; do
    ln -s /opt/orbstack-guest/bin/macctl /opt/orbstack-guest/bin/\$cmd
done

# Listeners for ssh-agent socket
# doesn't need to be created on every boot, and this avoids showing up in mount (because same fs as /opt/orbstack-guest)
mkdir -p /opt/orb/launchd-ssh-agent-listeners
ln -s /opt/orbstack-guest/run/host-ssh-agent.sock /opt/orb/launchd-ssh-agent-listeners/Listeners

# NFS state (because we removed nfs-utils package)
mkdir -p /var/lib/nfs /var/lib/nfs/v4recovery
touch /var/lib/nfs/etab /var/lib/nfs/rmtab /var/lib/nfs/state
EOT

# Scripts
COPY rootfs/utils/orb /opt/orb
COPY rootfs/utils/guest/etc /opt/orbstack-guest/etc
COPY LICENSE /

# Compiled binaries (in order of most likely to change)
COPY --from=c-build /out/* /opt/orb/
COPY --from=c-build /out-guest/* /opt/orbstack-guest/
# vcontrol
COPY --from=rust-build /out/vcontrol /opt/orb/
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
mkfs.erofs /images/rootfs.img /rootfs -z \$comp_alg
EOT

# speed up exporting images by skipping /rootfs
FROM packer as images
COPY --from=pack-images /images /images
