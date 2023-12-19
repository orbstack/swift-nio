#!/usr/bin/env bash

set -eufo pipefail
set -o xtrace

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

docker run -i --rm alpine ls
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
docker exec -i testa ls && exit 1 || :
docker start testa
docker exec -i testa ls
docker exec testa ls
docker ps
docker ps -a
docker cp testa:/etc/hostname $TMP/
docker cp $TMP/hostname testa:/tmp/
docker exec testa cat /tmp/hostname

docker commit testa testb
docker images
docker run --rm testb cat /tmp/hostname
docker export testa > $TMP/testb.tar
docker import $TMP/testb.tar testc
docker images
docker run --rm testc cat /tmp/hostname
docker rm -f testa

docker version
docker info
docker build .
docker images
docker pull ubuntu
docker search ubuntu
docker ps
docker run -d --name test nginx
docker exec test ls -l /usr/share/nginx/html
docker cp test:/usr/share/nginx/html/index.html $TMP/
docker cp $TMP/index.html test:/usr/share/nginx/html/index.html-1
docker inspect test
docker network list
#docker events
docker history nginx
docker logs test
docker port test
docker stats -a --no-stream
docker top test ps
docker tag ubuntu:latest ubuntu:test
docker restart test
docker commit test dockertest:test
docker save -o $TMP/dockertest.tar dockertest:test
docker load -i $TMP/dockertest.tar
docker export test > $TMP/test.tar
docker import $TMP/test.tar dockertest:test2
docker rename test test1
docker pause test1
docker unpause test1
docker stop test1
docker start test1
docker kill test1
docker rm test1
docker rmi ubuntu:test
docker rmi dockertest:test dockertest:test2
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
