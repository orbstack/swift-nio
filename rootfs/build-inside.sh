set -eo pipefail

IS_RELEASE=false

echo nameserver 1.1.1.1 > /etc/resolv.conf
apk add socat openrc bash libstdc++ dash chrony sfdisk xfsprogs xfsprogs-extra sshfs eudev
#lxd lxd-client

# new lxd 5.8 builds, patched to disable xfs quota
# dqlite patched to stop 500 ms heartbeat
apk add /packages/dqlite-1* /packages/lxd-5* /packages/lxd-client-5* /packages/lxd-openrc-5*

# only keep xfs_growfs from extra, remove python3
cp -a /usr/sbin/xfs_growfs /usr/sbin/xfs_quota /
apk del xfsprogs-extra
mv /xfs_growfs /usr/sbin/xfs_growfs
mv /xfs_quota /usr/sbin/xfs_quota

# remove useless getty instances
sed -i '/getty/d' /etc/inittab

if ! $IS_RELEASE; then
    echo 'hvc0::respawn:/sbin/agetty -L hvc0 115200 vt100 --autologin root' >> /etc/inittab
    apk add neovim iperf3 iproute2 agetty openssh tmux htop strace curl evtest powertop sysstat xfsprogs-extra quota-tools util-linux tcpdump ethtool mtr
    rc-update add sshd default
fi

# after debug shell
echo '::sysinit:/opt/vc/vinit-early' >> /etc/inittab
echo '::wait:/opt/vc/vinit-late' >> /etc/inittab

mkdir /root/.ssh
echo 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKE7Zy5HlH2BhRzz23wfmoO0LsSoxOfX0saf6HiL5c/c
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBWbetK7Sysq0tmjM0Hr7pwBupdEgoyDme2bcU/K30BG
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL+9Oxe4UXm5wNkkT0dx07HGFN6eqjIFzMx98oWSPCPt
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ/wCg/nWi0s+OYvjdW6JdxYaXpoO/fZvzwu0RRszPir' > /root/.ssh/authorized_keys

# services
rc-update add devfs
rc-update add sysfs
rc-update add networking default
rc-update add udev default
rc-update add lxd default
rc-update add chronyd default
touch /etc/network/interfaces

# spped up boot
echo 'rc_parallel="YES"' >> /etc/rc.conf
echo 'rc_need="localmount"' >> /etc/conf.d/sshd
rm -f /bin/sh
# dash is slightly faster
ln -s /usr/bin/dash /bin/sh

# lxd tuning
echo 'rc_cgroup_mode="unified"' >> /etc/rc.conf
# systemd cgroup is no longer needed. mounting it causes lxc to not detect cgroup v2
sed -i 's/systemd_ctr mount/:/' /etc/init.d/lxd
sed -i 's/systemd_ctr unmount/:/' /etc/init.d/lxd
echo '
* soft nofile 1048576
* hard nofile 1048576
* soft memlock unlimited
* hard memlock unlimited
' >> /etc/security/limits.conf
echo '
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

# lxd net tuning (= min tcp_mem)
net.core.netdev_max_backlog=13116

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
# echo '/dev/vdb1 /data ext4 rw,noatime,discard 0 1' >> /etc/fstab
#echo '/dev/vdb1 /data xfs rw,noatime,discard 0 0' >> /etc/fstab
echo '/dev/vdb1 /data xfs rw,noatime,discard,pquota 0 0' >> /etc/fstab
# for LXD image import
echo 'tmpfs /tmp tmpfs rw,noatime 0 0' >> /etc/fstab

# accurate time (PTP KVM clock)
# sync every 64 sec
echo 'refclock PHC /dev/ptp0 poll 6 dpoll 6' > /etc/chrony/chrony.conf
echo 'makestep 1.0 2' >> /etc/chrony/chrony.conf
echo 'cmdport 0' >> /etc/chrony/chrony.conf

# HACK: fix usbip ld lib path
mkdir /usbip
ln -s /opt/vc/usbip /usbip/prefix

# mounts
mkdir /mnt/android
mkdir /mnt/sdcard # for bind mount

# prep for data volume
mkdir /data
mv /var /data
ln -s /data/var /var

mkdir -p /data/root/.config
ln -s /data/root/.config /root/.config

mkdir /data/etc
mv /etc/resolv.conf /data/etc
ln -s /data/etc/resolv.conf /etc/resolv.conf

mkdir /data/etc/ssh
if ! $IS_RELEASE; then
    for f in ssh_host_dsa_key ssh_host_dsa_key.pub ssh_host_ecdsa_key ssh_host_ecdsa_key.pub ssh_host_ed25519_key ssh_host_ed25519_key.pub ssh_host_rsa_key ssh_host_rsa_key.pub
    do
        ln -s /data/etc/ssh/$f /etc/ssh/$f
    done
fi

# v1: initial
# v2: changed shared-sdcard mount source
# v3: moved security.nesting=true, security.privileged=true, device shared-sdcard to default profile; added devnode-ppp device
# v4: fixed each container storage having a separate project quota id (modded lxd), enable quota at upgrade
# TODO: update lxd-preseed with v3 profile
echo 3 > /data/version
