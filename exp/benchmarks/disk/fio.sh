#!/usr/bin/env bash

if [[ "$(uname)" == "Linux" ]]; then
  ioengine=io_uring
else
  ioengine=posixaio
fi

trap "rm -f fio.tmp" EXIT
fio --size=1000m --filename=fio.tmp --stonewall --ioengine=$ioengine --direct=1 \
  --name=seq1m-q8-read --bs=1m --iodepth=8 --rw=read \
  --name=seq1m-q8-write --bs=1m --iodepth=8 --rw=write \
  --name=seq1m-q1-read --bs=1m --iodepth=1 --rw=read \
  --name=seq1m-q1-write --bs=1m --iodepth=1 --rw=write \
  --name=rand4k-q32-read --bs=4k --iodepth=32 --rw=randread \
  --name=rand4k-q32-write --bs=4k --iodepth=32 --rw=randwrite \
  --name=rand4k-q1-read --bs=4k --iodepth=1 --rw=randread \
  --name=rand4k-q1-write --bs=4k --iodepth=1 --rw=randwrite
