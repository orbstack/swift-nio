set -eo pipefail
# ref: https://github.com/docker-library/docker/blob/master/20.10/dind/Dockerfile

echo nameserver 1.1.1.1 > /etc/resolv.conf
apk add --no-cache \
        docker-engine \
		btrfs-progs \
		e2fsprogs \
		e2fsprogs-extra \
		ip6tables \
		iptables \
		openssl \
		shadow-uidmap \
		xz \
		pigz \

# removed: xfsprogs

# dind script
wget -O /usr/local/bin/dind https://raw.githubusercontent.com/moby/moby/b54af02b51b0dfe2e2863e8f1647aaa27936c274/hack/dind
chmod +x /usr/local/bin/dind

# enable buildkit
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<EOF
{
  "features": {
    "buildkit" : true
  }
}
EOF

# set up subuid/subgid so that "--userns-remap=default" works out-of-the-box
addgroup -S dockremap
adduser -S -G dockremap dockremap
echo 'dockremap:165536:65536' >> /etc/subuid
echo 'dockremap:165536:65536' >> /etc/subgid

# network
echo 'nameserver 172.30.30.200' > /etc/resolv.conf
echo docker > /etc/hostname
echo '127.0.1.1 docker' >> /etc/hosts

# mounts
mkdir /mnt/mac
mkdir /opt/macvirt-guest
mkdir /var/lib/docker
