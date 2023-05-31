package images

import (
	"runtime"

	"golang.org/x/exp/slices"
)

const (
	ImageAlpine    = "alpine"
	ImageArch      = "archlinux"
	ImageCentos    = "centos"
	ImageDebian    = "debian"
	ImageFedora    = "fedora"
	ImageGentoo    = "gentoo"
	ImageKali      = "kali"
	ImageOpeneuler = "openeuler"
	ImageOpensuse  = "opensuse"
	ImageUbuntu    = "ubuntu"
	ImageVoid      = "voidlinux"

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
	DistroAlpine    = "alpine"
	DistroArch      = "arch"
	DistroCentos    = "centos"
	DistroDebian    = "debian"
	DistroFedora    = "fedora"
	DistroGentoo    = "gentoo"
	DistroKali      = "kali"
	DistroOpeneuler = "openeuler"
	DistroOpensuse  = "opensuse"
	DistroUbuntu    = "ubuntu"
	DistroVoid      = "void"

	DistroDevuan = "devuan"
	DistroAlma   = "alma"
	//DistroAmazon = "amazon"
	DistroOracle = "oracle"
	DistroRocky  = "rocky"

	// extra
	DistroNixos = "nixos"
)

var (
	DistroToImage = map[string]string{
		DistroAlpine:    ImageAlpine,
		DistroArch:      ImageArch,
		DistroCentos:    ImageCentos,
		DistroDebian:    ImageDebian,
		DistroFedora:    ImageFedora,
		DistroGentoo:    ImageGentoo,
		DistroKali:      ImageKali,
		DistroOpeneuler: ImageOpeneuler,
		DistroOpensuse:  ImageOpensuse,
		DistroUbuntu:    ImageUbuntu,
		DistroVoid:      ImageVoid,

		DistroDevuan: ImageDevuan,
		DistroAlma:   ImageAlma,
		// broken. requires cgroup v1, and network is broken
		//DistroAmazon: ImageAmazon,
		DistroOracle: ImageOracle,
		DistroRocky:  ImageRocky,

		// extra
		DistroNixos: ImageNixos,
	}

	ImageToLatestVersion = map[string]string{
		ImageAlpine:    "3.17",
		ImageArch:      "current",
		ImageCentos:    "9-Stream",
		ImageDebian:    "bullseye",
		ImageFedora:    "38",
		ImageGentoo:    "current",
		ImageKali:      "current",
		ImageOpeneuler: "23.03",
		ImageOpensuse:  "15.4",
		ImageUbuntu:    "lunar",
		ImageVoid:      "current",

		ImageDevuan: "chimaera",
		ImageAlma:   "9",
		//ImageAmazon: "current",
		ImageOracle: "9",
		ImageRocky:  "9",

		ImageNixos: "22.11",
	}

	// everything else is "default"
	ImageToDefaultVariant = map[string]string{
		// default and recommended over systemd
		ImageGentoo: "openrc",
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
