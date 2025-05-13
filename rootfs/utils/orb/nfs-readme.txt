# OrbStack file sharing

When OrbStack is running, this folder contains containers, images, volumes, and machines. All Docker and Linux files can be found here.

This is only a *view* of OrbStack's data; it takes no space on disk, and data is not actually stored here. The default data location is: ~/Library/Group Containers/HUAQ24HBR6.dev.orbstack/data

The folder is empty when OrbStack is not running. Do not put files here.

Learn more:
    - https://orb.cx/orbstack-folder
    - https://orb.cx/docker-mount
    - https://orb.cx/machine-mount


## Docker

OrbStack uses standard Docker named volumes.

Create a volume: `docker volume create foo`
Mount into a container: `docker run -v foo:/bar ...`
    - Use the volume name to mount it. DO NOT use ~/OrbStack here!
See files from Mac: `open ~/OrbStack/docker/volumes/foo`


---

[OrbStack is currently RUNNING. Files are available.]
