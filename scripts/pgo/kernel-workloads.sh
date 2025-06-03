#!/usr/bin/env bash

set -euxo pipefail

cd "$(dirname "$0")/../../"
repo_root="$(pwd)"

# NOTE: Docker IPv6 must be enabled!
# WARNING: k8s data will be deleted!

# Instrumented PGO (as compared to sampling AutoFDO) doesn't care about how *long* this stuff runs for, as long as it hits all the relevant code paths. So a lot of this can be sped up.
# we also get data from boot, struct page init, erofs loading on boot, etc.
# TODO: so remove hyperfine from most of these, reduce iperf durations

HYPERFINE="hyperfine -M 3"

echo "BEGIN PGO BENCHMARK AT"
date
echo

iperf3 -s &
iperf_server_pid=$!

# all runs with 12 GiB of memory

host_tmp="$(mktemp -d)"
cd "$host_tmp"
trap 'rm -fr "$host_tmp"' EXIT

# start with k8s off
orb stop k8s

# create machine
orb delete -f pgotest || :
orb create -u pgobuild debian:trixie pgotest
orb -m pgotest sudo DEBIAN_FRONTEND=noninteractive apt install -y build-essential bc cpio pahole pixz libjemalloc2 libelf-dev libssl-dev flex bison lz4 python3 git ripgrep iperf3 hyperfine fd-find rt-tests hey rustup bpftool fio
# prime ssh key for git
orb -m pgotest ssh -o StrictHostKeyChecking=no git@github.com || :
orb -m pgotest -w "/home/pgobuild" git clone --depth 1 git@github.com:orbstack/linux-macvirt-priv.git linux

# parallel btrfs ripgrep, uncached
orb -m pgotest -w "/home/pgobuild/linux" sudo $HYPERFINE -p 'echo 3 > /proc/sys/vm/drop_caches' 'rg hvc_put; fdfind -uuu'

# parallel ripgrep, cached
orb -m pgotest -w "/home/pgobuild/linux" $HYPERFINE 'rg hvc_put; fdfind -uuu'

# serial ripgrep, cached
orb -m pgotest -w "/home/pgobuild/linux" $HYPERFINE 'rg -j1 hvc_put; fdfind -j1 -uuu'

# hackbench
orb -m pgotest $HYPERFINE 'hackbench -pTl 8000'

# kernel build
# this also stresses madvise(MADV_DONTNEED) thanks to jemalloc LD_PRELOAD
# and mprotect contention
orb -m pgotest -s 'cd ~/linux && source setup.sh && rm -fr o2 && mkdir o2 && cp configs/arm64 o2/.config && marm O=o2'

# pnpm on virtiofs
cd "$repo_root/exp/fsbench/npm"
# arch linux arm pnpm is broken (incompatible node.js version)
# run twice: one uncached, one cached
docker run -i --rm -v $PWD:/data -w /data node sh -c 'rm -fr node_modules .pnpm-store && npm install -g pnpm && pnpm i && rm -fr node_modules && pnpm i'

# npm on virtiofs, in docker to stress seccomp
docker run -i --rm -v $PWD:/data -w /data node sh -c 'rm -fr node_modules && npm i --force'

# npm on overlayfs
docker run -i --rm -v $PWD:/data node sh -c 'cp /data/package.json . && npm i --force'

# ripgrep on virtiofs
cd "$repo_root/exp/fsbench/npm/node_modules"
orb -m pgotest sudo $HYPERFINE -p 'echo 3 > /proc/sys/vm/drop_caches' 'rg webp; fdfind -uuu'

# bpftool on virtiofs
cd "$host_tmp"
orb -m pgotest sudo $HYPERFINE "bpftool btf dump file /sys/kernel/btf/vmlinux format c > bpftool.txt"

# docker container startup
$HYPERFINE "docker run -i --rm -v $PWD:/data alpine echo"

# procfs
orb -m pgotest $HYPERFINE 'ps awux'

# machine restart
$HYPERFINE 'orb restart pgotest'

# network in machines
# iperf, ipv4, TCP, send
orb -m pgotest iperf3 -c host.orb.internal -4
# iperf, ipv4, TCP, receive
orb -m pgotest iperf3 -c host.orb.internal -4 -R
# iperf, ipv6, TCP, send
orb -m pgotest iperf3 -c host.orb.internal -6
# iperf, ipv6, TCP, receive
orb -m pgotest iperf3 -c host.orb.internal -6 -R
# iperf, ipv4, UDP, send
orb -m pgotest iperf3 -c host.orb.internal -4 -u -b 2000M
# iperf, ipv4, UDP, receive
orb -m pgotest iperf3 -c host.orb.internal -4 -u -R -b 2000M
# iperf, ipv6, UDP, send
orb -m pgotest iperf3 -c host.orb.internal -6 -u -b 2000M
# iperf, ipv6, UDP, receive
orb -m pgotest iperf3 -c host.orb.internal -6 -u -R -b 2000M

# network in containers
docker rm -f iperf-client-test || :
docker run -d --rm --name iperf-client-test alpine sleep inf
docker exec -i iperf-client-test apk add iperf3
# iperf, ipv4, TCP, send
docker exec -i iperf-client-test iperf3 -c host.orb.internal -4
# iperf, ipv4, TCP, receive
docker exec -i iperf-client-test iperf3 -c host.orb.internal -4 -R
# iperf, ipv6, TCP, send
docker exec -i iperf-client-test iperf3 -c host.orb.internal -6
# iperf, ipv6, TCP, receive
docker exec -i iperf-client-test iperf3 -c host.orb.internal -6 -R
# iperf, ipv4, UDP, send
docker exec -i iperf-client-test iperf3 -c host.orb.internal -4 -u -b 2000M
# iperf, ipv4, UDP, receive
docker exec -i iperf-client-test iperf3 -c host.orb.internal -4 -u -R -b 2000M
# iperf, ipv6, UDP, send
docker exec -i iperf-client-test iperf3 -c host.orb.internal -6 -u -b 2000M
# iperf, ipv6, UDP, receive
docker exec -i iperf-client-test iperf3 -c host.orb.internal -6 -u -R -b 2000M
# fq_codel net sched stress by spamming UDP on many vCPUs
docker exec -i iperf-client-test iperf3 -c host.orb.internal -u -b 10G -P 20
docker stop iperf-client-test

# pgbench on btrfs
cd "$repo_root/exp/fsbench/pgbench-btrfs"
make db
make pgbench
make clean

# pgbench on virtiofs
cd "$repo_root/exp/fsbench/pgbench-virtiofs"
rm -fr postgres-data || :
make db
make pgbench
make clean

# docker image build with rosetta
cd "$repo_root/rootfs"
docker builder prune -a -f
make amd64-debug

# NFS server
cd ~/OrbStack/pgotest/home/pgobuild/linux
$HYPERFINE 'rg hvc_put'

# memory pressure: run until OOM, 2x
cd "$repo_root"
# expected to fail (gets OOM killed)
orb -m pgotest exp/mem/memhog-linux || :
orb -m pgotest exp/mem/memhog-linux || :

# MGLRU aging + vfs cache pressure
# disable tty, so we can background this
ssh ovm apk add hyperfine
orb -m pgotest -w "/home/pgobuild/linux" $HYPERFINE 'rg -j1 hvc_put; fdfind -j1 -uuu' 2>&1 </dev/null | cat &
sleep 1
# same thing vinit writes
ssh ovm hyperfine -m 50 "'sleep 0.1; echo - 1 0 18446744073709551615 0 > /sys/kernel/debug/lru_gen; echo + 1 0 18446744073709551615 0 0 > /sys/kernel/debug/lru_gen'"

# run nginx for web tests
docker rm -f ngbench || :
docker run -d --rm --name ngbench -p 8123:80 nginx
# make sure server is up
sleep 2
# TLS proxy
hey -z 10s https://ngbench.orb.local
# agent userspace TCP proxy
hey -z 10s http://localhost:8123
# internally in VM
orb -m pgotest hey -z 10s http://docker.orb.internal:8123
docker stop ngbench

# stress pstub startup
docker run -i --rm -p 29000-29500:29000-29500 alpine echo

# stress orbstack tcp and udp userspace proxies
# this uses epoll and futex
docker rm -f iperf-test || :
docker run -d --rm --name iperf-test -p 5205:5201 -p 5205:5201/udp alpine sh -c 'apk add iperf3 && iperf3 -s'
# make sure server is up
sleep 5 # includes package downloads
# iperf, ipv4, TCP, send
iperf3 -c localhost -p 5205 -4
# iperf, ipv4, TCP, receive
iperf3 -c localhost -p 5205 -4 -R
# iperf, ipv6, TCP, send
iperf3 -c localhost -p 5205 -6
# iperf, ipv6, TCP, receive
iperf3 -c localhost -p 5205 -6 -R
# iperf, ipv4, UDP, send
iperf3 -c localhost -p 5205 -4 -u -b 2000M
# iperf, ipv4, UDP, receive
# TODO: fix this
#iperf3 -c localhost -p 5205 -4 -u -R -b 2000M
# iperf, ipv6, UDP, send
iperf3 -c localhost -p 5205 -6 -u -b 2000M
# iperf, ipv6, UDP, receive
# TODO: fix this
#iperf3 -c localhost -p 5205 -6 -u -R -b 2000M
docker stop iperf-test

# io_uring + fio + block stress
orb -m pgotest -w /home/pgobuild bash "$repo_root/exp/benchmarks/disk/fio.sh"

# virtiofs clone with tiny reads/writes
# TODO: create this on CI
cd /Volumes/CaseSensitive
rm -fr linux-ref-clone2
orb -m pgotest git clone linux-ref-clone linux-ref-clone2
rm -fr linux-ref-clone2

# TODO: non-PI futex stress

# singlestore DB (timerfd stress)
docker run -d --rm --platform linux/amd64 --name pgo-singlestore -e ROOT_PASSWORD=test ghcr.io/singlestore-labs/singlestoredb-dev:latest
sleep 30
docker kill pgo-singlestore

# rust build on virtiofs
cd "$repo_root/vinit"
rm -fr target
orb -m pgotest rustup update stable
orb -m pgotest cargo build

# clear k8s data (this disables k8s, so run it first)
orb delete -f k8s || :
# re-enable k8s
orb start k8s
sleep 15
# deploy something
helm repo add bitnami https://charts.bitnami.com/bitnami
helm repo update
helm install bitnami/mysql --generate-name
helm install minio oci://registry-1.docker.io/bitnamicharts/minio --set provisioning.enabled=true || :
sleep 5

# explicit drop_caches eviction
echo 3 | orb -m pgotest sudo tee /proc/sys/vm/drop_caches

# node.js app
# TODO: clone this on CI
cd ~/code/web/orbstack-web
docker compose down || :
docker compose up -d
# leave this running while we build posthog

# PostHog docker build (native and Rosetta)
# TODO: clone this on CI
cd ~/code/vm/posthog
docker build . -f production.Dockerfile --platform linux/arm64
docker build . -f production.Dockerfile --platform linux/amd64

# test and stop node.js app
cd ~/code/web/orbstack-web
curl https://orbstack.local/
docker compose down

# bring supabase up
# TODO: clone this on CI
cd ~/code/vm/supabase/docker
docker compose down || :
docker compose up -d
sleep 30
docker compose down

# machine delete
orb delete -f pgotest

echo "END PGO BENCHMARK AT"
date
echo

kill $iperf_server_pid
