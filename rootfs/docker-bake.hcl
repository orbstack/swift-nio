# set via env variables
variable "BTYPE" {
  default = "debug"
}
variable "ARCH" {
  default = "arm64"
}
variable "HOST_ARCH" {
  default = "arm64"
}
variable "PLATFORM" {
  default = "linux/arm64"
}
variable "VERSION" {
  default = "latest"
}

target "rootfs" {
  # note: dockerfile is relative to context
  dockerfile = "./rootfs/Dockerfile"
  context    = ".."
  args = {
    TYPE      = "${BTYPE}"
    ARCH      = "${ARCH}"
    HOST_ARCH = "${HOST_ARCH}"
  }
  ssh      = ["default"]
  platform = "${PLATFORM}"
  load     = true
  tags     = ["ghcr.io/orbstack/images:${BTYPE}"]
}


# run from root directory (see wormhole/publish.sh)
target "wormhole" {
  dockerfile = "./rootfs/Dockerfile"
  context    = "."
  args = {
    TYPE      = "${BTYPE}"
    ARCH      = "${ARCH}"
    HOST_ARCH = "${HOST_ARCH}"
  }
  target     = "wormhole-remote"
  ssh        = ["default"]
  tags       = ["registry.orb.local/wormhole:${VERSION}"]
}
