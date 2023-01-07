# syntax=docker/dockerfile:1

ARG IS_RELEASE=false

FROM alpine:edge AS final

# Early build nameserver
COPY config/resolv.conf /etc/resolv.conf

# Dependencies
RUN apk add --no-cache \
    socat \
    openrc \
    bash \
    libstdc++ \
    dash \
    chrony \
    sfdisk \
    xfsprogs \
    xfsprogs-extra \
    sshfs \
    eudev

# only keep xfs_growfs from extra, remove python3
RUN cp -a /usr/sbin/xfs_growfs /usr/sbin/xfs_quota / && \
    apk del xfsprogs-extra && \
    mv /xfs_growfs /usr/sbin/xfs_growfs && \
    mv /xfs_quota /usr/sbin/xfs_quota

# LXD 5.8 packages
# Patches: disable XFS quota, stop 500 ms dqlite heartbeat
COPY packages /buildtmp/packages
COPY packages/*.pub /etc/apk/keys/
RUN apk add --no-cache \
    /buildtmp/packages/dqlite-1* \
    /buildtmp/packages/lxd-5* \
    /buildtmp/packages/lxd-client-5* \
    /buildtmp/packages/lxd-openrc-5* \
    && rm -fr /buildtmp

# virtcontainer files
COPY vc /opt/vc
COPY LICENSE /opt/vc/

# Tools
# preinit
COPY rd-compile/switch_overlay_root /opt/vc/
# vcontrol
COPY bins/vcontrol /opt/vc/
# debugging
COPY rd-compile/eventstat/eventstat /opt/vc/

# Init services
RUN rc-update add devfs && \
    rc-update add sysfs && \
    rc-update add networking default && \
    rc-update add udev default && \
    rc-update add lxd default && \
    rc-update add chronyd default
RUN touch /etc/network/interfaces
COPY config/inittab /etc/inittab
# Speed up boot
RUN echo 'rc_parallel="YES"' >> /etc/rc.conf; \
    echo 'rc_need="localmount"' >> /etc/conf.d/sshd
# dash is slightly faster
RUN rm -f /bin/sh && ln -s /usr/bin/dash /bin/sh

# LXD tuning
RUN echo 'rc_cgroup_mode="unified"' >> /etc/rc.conf
# systemd cgroup is no longer needed. mounting it causes lxc to not detect cgroup v2
RUN sed -i 's/systemd_ctr mount/:/' /etc/init.d/lxd; \
    sed -i 's/systemd_ctr unmount/:/' /etc/init.d/lxd
COPY config/limits.conf /etc/security/limits.conf
COPY config/sysctl.conf /etc/sysctl.conf

# Fake sysctls
RUN mkdir -p /fake/sysctl; \
    echo 10 > /fake/sysctl/kernel.panic

# DNS (gvproxy server doesn't work on Android)
COPY config/udhcpc.conf /etc/udhcpc/udhcpc.conf

# Mounts
COPY config/fstab /etc/fstab
RUN mkdir /mnt/android && \
    mkdir /mnt/sdcard

# Time (sync KVM PTP clock every 64 sec)
COPY config/chrony.conf /etc/chrony/chrony.conf

# Data volume
RUN mkdir /data && \
    mv /var /data/ && \
    ln -s /data/var /var && \
    mkdir -p /data/root/.config && \
    ln -s /data/root/.config /root/.config && \
    mkdir /data/etc && \
    cp /etc/resolv.conf /data/etc && \
    ln -sf /data/etc/resolv.conf /etc/resolv.conf

# Debug tools
COPY config/authorized_keys /root/.ssh/authorized_keys
RUN [ "$IS_RELEASE" = "true" ] && sed -i '/getty/d' /etc/inittab || :
RUN [ "$IS_RELEASE" = "false" ] && (apk add neovim iperf3 iproute2 agetty openssh tmux htop strace curl evtest powertop sysstat xfsprogs-extra quota-tools util-linux && \
    rc-update add sshd default && \
    mkdir /data/etc/ssh && \
    (for f in ssh_host_dsa_key ssh_host_dsa_key.pub ssh_host_ecdsa_key ssh_host_ecdsa_key.pub ssh_host_ed25519_key ssh_host_ed25519_key.pub ssh_host_rsa_key ssh_host_rsa_key.pub; do \
        ln -s /data/etc/ssh/$f /etc/ssh/$f; \
    done)) || :

# Version
# v1: initial
# v2: changed shared-sdcard mount source
# v3: moved security.nesting=true, security.privileged=true, device shared-sdcard to default profile; added devnode-ppp device
# v4: fixed each container storage having a separate project quota id (modded lxd), enable quota at upgrade
# TODO: update lxd-preseed with v3 profile
RUN echo 3 > /data/version
