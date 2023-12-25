//
// Created by Danny Lin on 2/5/23.
//

import Foundation

struct DistroVersion: Equatable, Identifiable, Hashable {
    let key: String
    let friendlyName: String

    var id: String { key }
}

private func v(_ key: String, as friendlyName: String? = nil) -> DistroVersion {
    DistroVersion(key: key, friendlyName: friendlyName ?? key)
}

enum Distro: String, CaseIterable {
    case alma
    case alpine
    case arch
    case centos
    case debian
    case devuan
    case fedora
    case gentoo
    case kali
    case nixos
    case openeuler
    case opensuse
    case oracle
    case rocky
    case ubuntu
    case void

    var imageKey: String {
        switch self {
        case .alma: return "almalinux"
        case .alpine: return "alpine"
        case .arch: return "archlinux"
        case .centos: return "centos"
        case .debian: return "debian"
        case .devuan: return "devuan"
        case .fedora: return "fedora"
        case .gentoo: return "gentoo"
        case .kali: return "kali"
        case .nixos: return "nixos"
        case .openeuler: return "openeuler"
        case .opensuse: return "opensuse"
        case .oracle: return "oracle"
        case .rocky: return "rockylinux"
        case .ubuntu: return "ubuntu"
        case .void: return "voidlinux"
        }
    }

    var friendlyName: String {
        switch self {
        case .alma: return "Alma"
        case .alpine: return "Alpine"
        case .arch: return "Arch"
        case .centos: return "CentOS"
        case .debian: return "Debian"
        case .devuan: return "Devuan"
        case .fedora: return "Fedora"
        case .gentoo: return "Gentoo"
        case .kali: return "Kali"
        case .nixos: return "NixOS"
        case .openeuler: return "openEuler"
        case .opensuse: return "OpenSUSE"
        case .oracle: return "Oracle"
        case .rocky: return "Rocky"
        case .ubuntu: return "Ubuntu"
        case .void: return "Void"
        }
    }

    // last version is latest stable default
    // systemd cgroupv1 excluded: centos 7, ubuntu bionic, oracle 7
    var versions: [DistroVersion] {
        // DON'T FORGET TO UPDATE scon/images/images.go!
        switch self {
        case .alma: return [v("8"), v("9")]
        case .alpine: return [v("edge"), v("3.16"), v("3.17"), v("3.18"), v("3.19")]
        case .arch: return [v("current", as: "Latest")]
        case .centos: return [ /* v("7"), */ v("8-Stream", as: "8 (Stream)"), v("9-Stream", as: "9 (Stream)")]
        case .debian: return [v("buster", as: "10 (Buster)"), v("bullseye", as: "11 (Bullseye)"), v("trixie", as: "13 (Trixie, testing)"), v("sid", as: "Sid (unstable)"), v("bookworm", as: "12 (Bookworm)")]
        case .devuan: return [v("beowulf", as: "Beowulf"), v("chimaera", as: "Chimaera"), v("daedalus", as: "Daedalus")]
        case .fedora: return [v("37"), v("38"), v("39")]
        case .gentoo: return [v("current", as: "Latest")]
        case .kali: return [v("current", as: "Latest")]
        case .nixos: return [v("unstable", as: "Unstable"), v("23.11")]
        case .openeuler: return [v("20.03"), v("22.03"), v("23.03")]
        case .opensuse: return [v("tumbleweed", as: "Tumbleweed"), v("15.4"), v("15.5")]
        case .oracle: return [v("8"), v("9")]
        case .rocky: return [v("8"), v("9")]
        case .ubuntu: return [
                // v("xenial", as: "16.04 LTS (Xenial Xerus)"),
                v("bionic", as: "18.04 LTS (Bionic Beaver)"),
                v("focal", as: "20.04 LTS (Focal Fossa)"),
                v("jammy", as: "22.04 LTS (Jammy Jellyfish)"),
                v("lunar", as: "23.04 (Lunar Lobster)"),
                v("mantic", as: "23.10 (Mantic Minotaur)"),
            ]
        case .void: return [v("current", as: "Latest")]
        }
    }

    var hasCloudVariant: Bool {
        switch self {
        case .alma: return true
        case .alpine: return true
        // arch only has cloud for amd64
        case .centos: return true
        case .debian: return true
        case .devuan: return true
        case .fedora: return true
        case .kali: return true
        case .openeuler: return true
        case .opensuse: return true
        case .oracle: return true
        case .rocky: return true
        case .ubuntu: return true
        default: return false
        }
    }
}
