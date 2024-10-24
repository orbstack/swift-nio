#!/usr/bin/env bash

set -eufo pipefail
set -o xtrace

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

docker run -i --rm alpine ls
docker run --name "$1"alpine-ls --rm alpine ls
docker run --name "$1"alpine-ls  alpine ls
docker rm -f "$1"alpine-ls 
docker ps
docker ps -a
docker run -d --name "$1"testa alpine sleep 1000
docker ps
docker ps -a
docker stop "$1"testa
docker ps
docker ps -a
docker exec -i "$1"testa ls && exit 1 || :
docker start "$1"testa
docker exec -i "$1"testa ls
docker exec "$1"testa ls
docker ps
docker ps -a
docker cp "$1"testa:/etc/hostname $TMP/
docker cp $TMP/hostname "$1"testa:/tmp/
docker exec "$1"testa cat /tmp/hostname

docker commit "$1"testa "$1"testb
docker images
docker run --rm "$1"testb cat /tmp/hostname
docker export "$1"testa > $TMP/testb.tar
docker import $TMP/testb.tar "$1"testc
docker images
docker run --rm "$1"testc cat /tmp/hostname
docker rm -f "$1"testa

docker version
docker info
docker build .
docker images
docker pull ubuntu
docker search ubuntu
docker ps
docker run -d --name "$1"testd nginx
docker exec "$1"testd ls -l /usr/share/nginx/html
docker cp "$1"testd:/usr/share/nginx/html/index.html $TMP/
docker cp $TMP/index.html "$1"testd:/usr/share/nginx/html/index.html-1
docker inspect "$1"testd
docker network list
#docker events
docker history nginx
docker logs "$1"testd
docker port "$1"testd
docker stats -a --no-stream
docker top "$1"testd ps
docker tag ubuntu:latest ubuntu:"$1"test
docker restart "$1"testd
docker commit "$1"testd "$1"dockertest:test
docker save -o $TMP/dockertest.tar "$1"dockertest:test
docker load -i $TMP/dockertest.tar
docker export "$1"testd > $TMP/test.tar
docker import $TMP/test.tar "$1"dockertest:test2
docker rename "$1"testd "$1"testd1
docker pause "$1"testd1
docker unpause "$1"testd1
docker stop "$1"testd1
docker start "$1"testd1
docker kill "$1"testd1
docker rm "$1"testd1
docker rmi ubuntu:"$1"test
docker rmi "$1"dockertest:test "$1"dockertest:test2
docker volume ls


#  push        Upload an image to a registry
#  login       Log in to a registry
#  logout      Log out from a registry
#  search      Search Docker Hub for images

#Management Commands:
#  builder     Manage builds
#  buildx*     Docker Buildx (Docker Inc., v0.12.0)
#  compose*    Docker Compose (Docker Inc., v2.23.3)
#  container   Manage containers
#  context     Manage contexts
#  image       Manage images
#  manifest    Manage Docker image manifests and manifest lists
#  network     Manage networks
#  plugin      Manage plugins
#  system      Manage Docker
#  trust       Manage trust on Docker images
#  volume      Manage volumes

#Swarm Commands:
#  swarm       Manage Swarm

#Commands:
#  attach      Attach local standard input, output, and error streams to a running container
#  create      Create a new container
#  diff        Inspect changes to files or directories on a container's filesystem
#  export      Export a container's filesystem as a tar archive
#  import      Import the contents from a tarball to create a filesystem images
#  update      Update configuration of one or more containers
#  wait        Block until one or more containers stop, then print their exit codes
