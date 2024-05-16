#!/usr/bin/env bash

docker-compose up -d postgres
docker-compose run --rm wait-for-db
docker-compose run --rm pgbench-init
docker-compose run --rm pgbench
docker-compose down -v
