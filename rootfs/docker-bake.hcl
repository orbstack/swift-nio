# set via env variables
variable "BTYPE" {}
variable "ARCH" {}
variable "HOST_ARCH" {}
variable "PLATFORM" {}
variable "SSH_AUTH_SOCK" {}
variable "VERSION" {}

target "rootfs" {
  # note: dockerfile is relative to context
  dockerfile = "./rootfs/Dockerfile"
  context    = ".."
  args = {
    BTYPE     = "${BTYPE}"
    ARCH      = "${ARCH}"
    HOST_ARCH = "${HOST_ARCH}"
  }
  ssh      = ["default=${SSH_AUTH_SOCK}"]
  platform = "${PLATFORM}"
  load     = true
  tags     = ["ghcr.io/orbstack/images:${BTYPE}"]
}


# run from root directory (see wormhole/publish.sh)
target "wormhole" {
  dockerfile = "./rootfs/Dockerfile"
  context    = "."
  target     = "wormhole"
  ssh        = ["default=${SSH_AUTH_SOCK}"]
  tags       = ["wormhole:${VERSION}"]
}
