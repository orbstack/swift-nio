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

	// for testing only
	ImageToOldestVersion = map[string]string{
		ImageAlma:      "8",
		ImageAlpine:    "3.14",
		ImageArch:      "current",
		ImageCentos:    "8-Stream",
		ImageDebian:    "buster",
		ImageDevuan:    "beowulf",
		ImageFedora:    "36",
		ImageGentoo:    "current",
		ImageKali:      "current",
		ImageNixos:     "22.11",
		ImageOpeneuler: "20.03",
		ImageOpensuse:  "15.4",
		ImageOracle:    "8",
		ImageRocky:     "8",
		ImageUbuntu:    "bionic",
		ImageVoid:      "current",
	}

	ImageToLatestVersion = map[string]string{
		//ImageAmazon: "current",
		ImageAlma:      "9",
		ImageAlpine:    "3.18",
		ImageArch:      "current",
		ImageCentos:    "9-Stream",
		ImageDebian:    "bookworm",
		ImageDevuan:    "chimaera",
		ImageFedora:    "38",
		ImageGentoo:    "current",
		ImageKali:      "current",
		ImageNixos:     "23.05",
		ImageOpeneuler: "23.03",
		ImageOpensuse:  "15.5",
		ImageOracle:    "9",
		ImageRocky:     "9",
		ImageUbuntu:    "lunar",
		ImageVoid:      "current",
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
