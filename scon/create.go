package main

const (
	DistroAlpine   = "alpine"
	DistroArch     = "arch"
	DistroCentos   = "centos"
	DistroDebian   = "debian"
	DistroFedora   = "fedora"
	DistroGentoo   = "gentoo"
	DistroKali     = "kali"
	DistroOpensuse = "opensuse"
	DistroUbuntu   = "ubuntu"
	DistroVoid     = "void"
	DistroNixos    = "nixos"

	DistroDevuan  = "devuan"
	DistroAlma    = "alma"
	DistroAmazon  = "amazon"
	DistroApertis = "apertis"
	DistroOracle  = "oracle"
	DistroRocky   = "rocky"

	ImageAlpine   = "alpine"
	ImageArch     = "archlinux"
	ImageCentos   = "centos"
	ImageDebian   = "debian"
	ImageFedora   = "fedora"
	ImageGentoo   = "gentoo"
	ImageKali     = "kali"
	ImageOpensuse = "opensuse"
	ImageUbuntu   = "ubuntu"
	ImageVoid     = "voidlinux"
	ImageNixos    = "nixos"

	ImageDevuan  = "devuan"
	ImageAlma    = "almalinux"
	ImageAmazon  = "amazonlinux"
	ImageApertis = "apertis"
	ImageOracle  = "oracle"
	ImageRocky   = "rockylinux"
)

type ImageSpec struct {
	Distro  string
	Version string
	Arch    string
	Variant string
}
