package images

import (
	"runtime"

	"golang.org/x/exp/slices"
)

const (
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

	ImageDevuan = "devuan"
	ImageAlma   = "almalinux"
	//ImageAmazon = "amazonlinux"
	ImageOracle = "oracle"
	ImageRocky  = "rockylinux"

	// extra
	ImageNixos  = "nixos"
	ImageDocker = "docker" // can't be created
	//ImageUbuntuFull = "ubuntu-full" // not yet supported
)

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

	DistroDevuan = "devuan"
	DistroAlma   = "alma"
	//DistroAmazon = "amazon"
	DistroOracle = "oracle"
	DistroRocky  = "rocky"

	// extra
	//DistroNixos = "nixos"
)

var (
	DistroToImage = map[string]string{
		DistroAlpine:   ImageAlpine,
		DistroArch:     ImageArch,
		DistroCentos:   ImageCentos,
		DistroDebian:   ImageDebian,
		DistroFedora:   ImageFedora,
		DistroGentoo:   ImageGentoo,
		DistroKali:     ImageKali,
		DistroOpensuse: ImageOpensuse,
		DistroUbuntu:   ImageUbuntu,
		DistroVoid:     ImageVoid,

		DistroDevuan: ImageDevuan,
		DistroAlma:   ImageAlma,
		// broken. requires cgroup v1, and network is broken
		//DistroAmazon: ImageAmazon,
		DistroOracle: ImageOracle,
		DistroRocky:  ImageRocky,

		// extra
		// TODO support nixos
		//DistroNixos: ImageNixos,
	}

	DistroToLatestVersion = map[string]string{
		DistroAlpine:   "3.17",
		DistroArch:     "current",
		DistroCentos:   "9-Stream",
		DistroDebian:   "bullseye",
		DistroFedora:   "37",
		DistroGentoo:   "current",
		DistroKali:     "current",
		DistroOpensuse: "15.4",
		DistroUbuntu:   "kinetic",
		DistroVoid:     "current",

		DistroDevuan: "chimaera",
		DistroAlma:   "9",
		//DistroAmazon: "current",
		DistroOracle: "9",
		DistroRocky:  "9",

		// TODO support nixos
		//DistroNixos: "22.11",
	}

	// everything else is "default"
	DistroToDefaultVariant = map[string]string{
		// default and recommended over systemd
		DistroGentoo: "openrc",
	}
)

func Distros() []string {
	var distros []string
	for distro := range DistroToImage {
		distros = append(distros, distro)
	}
	slices.Sort(distros)
	return distros
}

func Archs() []string {
	switch runtime.GOARCH {
	case "amd64":
		return []string{"amd64", "i386"}
	case "arm64":
		return []string{"arm64", "amd64"}
	default:
		panic("unsupported architecture")
	}
}

func NativeArch() string {
	switch runtime.GOARCH {
	case "i386":
		return "i386"
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "arm":
		return "armhf"
	default:
		panic("unsupported architecture")
	}
}
