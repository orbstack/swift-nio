# OrbStack file sharing

When OrbStack is running, this folder contains Docker volumes and Linux machines. All Docker and Linux files can be found here.

This folder is empty when OrbStack is not running. Do not put files here.

For more details, see:
    - https://go.orbstack.dev/docker-mount
    - https://go.orbstack.dev/machine-mount


## Docker

OrbStack uses standard Docker named volumes.

Create a volume: `docker volume create foo`
Mount into a container: `docker run -v foo:/bar ...`
    - Use the volume name to mount it. DO NOT use ~/OrbStack here!
See files from Mac: `open ~/OrbStack/docker/volumes/foo`

Learn more: https://go.orbstack.dev/docker-mount


---

[OrbStack is RUNNING. Files are available.]
