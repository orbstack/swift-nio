set -eo pipefail

#IS_RELEASE=false

echo nameserver 1.1.1.1 > /etc/resolv.conf
# 1. basic
# 2. disk - util-linux to fix "/dev/vda: Can't open blockdev" for btrfs mount
# 3. scon deps
apk add --no-cache \
    socat openrc bash libstdc++ dash chrony eudev \
    sfdisk nfs-utils btrfs-progs util-linux \
    lxc-libs tar squashfs-tools ca-certificates dnsmasq iptables ip6tables xz

# remove useless getty instances
sed -i '/getty/d' /etc/inittab

if ! $IS_RELEASE; then
    echo 'hvc0::respawn:/sbin/agetty -L hvc0 115200 vt100 --autologin root' >> /etc/inittab
    apk add neovim iperf3 iproute2 agetty openssh tmux htop strace curl evtest powertop sysstat quota-tools util-linux tcpdump ethtool mtr ksmbd-tools bind e2fsprogs-extra btrfs-progs-extra f2fs-tools fish
    rc-update add sshd default
    sed -i 's|/bin/ash|/usr/bin/fish|' /etc/passwd

    mkdir /root/.ssh
echo 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKE7Zy5HlH2BhRzz23wfmoO0LsSoxOfX0saf6HiL5c/c
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBWbetK7Sysq0tmjM0Hr7pwBupdEgoyDme2bcU/K30BG
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL+9Oxe4UXm5wNkkT0dx07HGFN6eqjIFzMx98oWSPCPt
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ/wCg/nWi0s+OYvjdW6JdxYaXpoO/fZvzwu0RRszPir' > /root/.ssh/authorized_keys
fi

# after debug shell
echo '::sysinit:/opt/vc/vinit-early' >> /etc/inittab
echo '::wait:/opt/vc/vinit-late' >> /etc/inittab

# services
rc-update add devfs
rc-update add sysfs
rc-update add cgroups default
rc-update add networking default
rc-update add udev default
rc-update add chronyd default
touch /etc/network/interfaces

# spped up boot
echo 'rc_parallel="YES"' >> /etc/rc.conf
echo 'rc_need="localmount"' >> /etc/conf.d/sshd
rm -f /bin/sh
# dash is slightly faster
ln -s /usr/bin/dash /bin/sh

# LXD / scon container tuning
# must exist for liblxc startup
mkdir -p /usr/lib/lxc/rootfs
echo 'rc_cgroup_mode="unified"' >> /etc/rc.conf
echo '
#
# Copyright 2022-2023 Danny Lin <danny@kdrag0n.dev>. All rights reserved.
# 
# Unauthorized copying of this software and associated documentation files (the "Software"), via any medium, is strictly prohibited. Confidential and proprietary.
# 
# The above copyright notice shall be included in all copies or substantial portions of the Software.
#

# idle cpu
vm.compaction_proactiveness=0
vm.stat_interval=30

# res limits
kernel.pid_max=4194304
fs.file-max=1048576

# lxd recommended
fs.aio-max-nr=524288
fs.inotify.max_queued_events=1048576
fs.inotify.max_user_instances=1048576
fs.inotify.max_user_watches=1048576
# no point for this use case
#kernel.dmesg_restrict=1
kernel.keys.maxbytes=2000000
kernel.keys.maxkeys=2000
net.ipv4.neigh.default.gc_thresh3=8192
net.ipv6.neigh.default.gc_thresh3=8192
vm.max_map_count=262144

# lxd net tuning (= ~min tcp_mem)
net.core.netdev_max_backlog=16384

# k8s
vm.overcommit_memory=1
vm.panic_on_oom=0
kernel.panic_on_oops=1
# fake this one
#kernel.panic=10
kernel.keys.root_maxkeys=1000000
kernel.keys.root_maxbytes=25000000

# redis https://docs.bitnami.com/kubernetes/infrastructure/redis-cluster/administration/configure-kernel-settings/
net.core.somaxconn=10000

# unpriv ping
net.ipv4.ping_group_range=0 2147483647

# scon net
net.ipv4.ip_forward=1
net.ipv6.conf.all.forwarding=1

# security
fs.protected_hardlinks=1
fs.protected_symlinks=1
' >> /etc/sysctl.conf

# fake sysctls
mkdir -p /fake/sysctl
echo 10 > /fake/sysctl/kernel.panic

# DNS (gvproxy server doesn't work on Android)
mkdir /etc/udhcpc
echo 'dns="1.0.0.1 1.1.1.1"' > /etc/udhcpc/udhcpc.conf
echo 'RESOLV_CONF="no"' >> /etc/udhcpc/udhcpc.conf

# fstab
echo '/dev/vda / erofs rw,noatime 0 0' > /etc/fstab
echo '/dev/vdb1 /data ext4 rw,noatime,discard,prjquota 0 0' >> /etc/fstab
#echo '/dev/vdb1 /data xfs rw,noatime,discard 0 0' >> /etc/fstab
#echo '/dev/vdb1 /data xfs rw,noatime,discard,pquota 0 0' >> /etc/fstab
#echo '/dev/vdb1 /data f2fs rw,noatime,discard,prjquota,atgc,gc_merge 0 1' >> /etc/fstab
# echo '/dev/vdb1 /data btrfs rw,noatime,discard=async,space_cache=v2,ssd,nodatacow,nodatasum' >> /etc/fstab
# for LXD image import
echo 'tmpfs /tmp tmpfs rw,noatime 0 0' >> /etc/fstab

# accurate time (PTP KVM clock)
# sync every 128 sec after init/suspendresume
echo 'server 172.30.30.200 iburst minpoll 7' > /etc/chrony/chrony.conf
# always step clock if needed
echo 'makestep 3.0 -1' >> /etc/chrony/chrony.conf
echo 'cmdport 0' >> /etc/chrony/chrony.conf

# prod config
echo nameserver 172.30.30.200 > /etc/resolv.conf
rm -f /etc/motd

# NFS
echo '/nfsroot-ro 127.0.0.8(rw,async,fsid=0,crossmnt,insecure,all_squash,no_subtree_check,anonuid=0,anongid=0)' > /etc/exports
# 32 threads for perf
echo 'OPTS_RPC_NFSD="32"' >> /etc/conf.d/nfs
# fix fd hang
echo 'OPTS_NFSD="nfsv4leasetime=30 nfsv4gracetime=1"' >> /etc/conf.d/nfs
mkdir /nfsroot-ro /nfsroot-rw

# hostname
echo vchost > /etc/hostname
echo '127.0.1.1 vchost' >> /etc/hosts

# HACK: fix usbip ld lib path
mkdir /usbip
ln -s /opt/vc/usbip /usbip/prefix

# mounts
mkdir /mnt/mac
mkdir /mnt/rosetta
mkdir /mnt/guest-tools

# guest tools
mkdir -p /opt/macvirt-guest/bin /opt/macvirt-guest/bin-hiprio /opt/macvirt-guest/run /opt/macvirt-guest/data
ln -s /opt/macvirt-guest/bin/macctl /opt/macvirt-guest/bin/mac
# default cmd links
for cmd in open; do
    ln -s /opt/macvirt-guest/bin/macctl /opt/macvirt-guest/bin-hiprio/$cmd
done
for cmd in osascript code; do
    ln -s /opt/macvirt-guest/bin/macctl /opt/macvirt-guest/bin/$cmd
done

# prep for data volume
mkdir /data
mkdir -p /data/guest-state/bin/cmdlinks

# v1: initial
echo 1 > /data/version
