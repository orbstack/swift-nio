#!/usr/bin/env bash

set -eufo pipefail

docker run -it --rm alpine ls
docker run -i --rm alpine ls
docker run -t --rm alpine ls
docker run --rm alpine ls
docker run alpine ls
docker ps
docker ps -a
docker run -d --name testa alpine sleep 1000
docker ps
docker ps -a
docker stop testa
docker ps
docker ps -a
docker exec -it testa ls && exit 1 || :
docker start testa
docker exec -it testa ls
docker exec -i testa ls
docker exec -t testa ls
docker exec testa ls
docker ps
docker ps -a
docker cp testa:/etc/hostname /tmp/
docker cp /tmp/hostname testa:/tmp/
docker exec testa cat /tmp/hostname

docker commit testa testb
docker images
docker run --rm testb cat /tmp/hostname
docker export testa > /tmp/testb.tar
docker import /tmp/testb.tar testc
docker images
docker run --rm testc cat /tmp/hostname
docker rm -f testa
