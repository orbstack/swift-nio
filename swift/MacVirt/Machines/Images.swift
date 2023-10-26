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
    case alma   = "alma"
    case alpine   = "alpine"
    case arch     = "arch"
    case centos   = "centos"
    case debian   = "debian"
    case devuan = "devuan"
    case fedora   = "fedora"
    case gentoo   = "gentoo"
    case kali     = "kali"
    case nixos = "nixos"
    case openeuler = "openeuler"
    case opensuse = "opensuse"
    case oracle = "oracle"
    case rocky  = "rocky"
    case ubuntu   = "ubuntu"
    case void     = "void"

    var imageKey: String {
        switch self {
        case .alma:   return "almalinux"
        case .alpine:   return "alpine"
        case .arch:     return "archlinux"
        case .centos:   return "centos"
        case .debian:   return "debian"
        case .devuan: return "devuan"
        case .fedora:   return "fedora"
        case .gentoo:   return "gentoo"
        case .kali:     return "kali"
        case .nixos: return "nixos"
        case .openeuler: return "openeuler"
        case .opensuse: return "opensuse"
        case .oracle: return "oracle"
        case .rocky:  return "rockylinux"
        case .ubuntu:   return "ubuntu"
        case .void:     return "voidlinux"
        }
    }

    var friendlyName: String {
        switch self {
        case .alma:   return "Alma"
        case .alpine:   return "Alpine"
        case .arch:     return "Arch"
        case .centos:   return "CentOS"
        case .debian:   return "Debian"
        case .devuan: return "Devuan"
        case .fedora:   return "Fedora"
        case .gentoo:   return "Gentoo"
        case .kali:     return "Kali"
        case .nixos: return "NixOS"
        case .openeuler: return "openEuler"
        case .opensuse: return "OpenSUSE"
        case .oracle: return "Oracle"
        case .rocky:  return "Rocky"
        case .ubuntu:   return "Ubuntu"
        case .void:     return "Void"
        }
    }

    // last version is latest stable default
    // systemd cgroupv1 excluded: centos 7, ubuntu bionic, oracle 7
    var versions: [DistroVersion] {
        // DON'T FORGET TO UPDATE scon/images/images.go!
        switch self {
        case .alma:   return [v("8"), v("9")]
        case .alpine:   return [v("edge"), v("3.15"), v("3.16"), v("3.17"), v("3.18")]
        case .arch:     return [v("current", as: "Latest")]
        case .centos:   return [/*v("7"),*/ v("8-Stream", as: "8 (Stream)"), v("9-Stream", as: "9 (Stream)")]
        case .debian:   return [v("buster", as: "10 (Buster)"), v("bullseye", as: "11 (Bullseye)"), v("sid", as: "Sid (unstable)"), v("bookworm", as: "12 (Bookworm)")]
        case .devuan: return [v("beowulf", as: "Beowulf"), v("chimaera", as: "Chimaera"), v("daedalus", as: "Daedalus")]
        case .fedora:   return [v("37"), v("38")]
        case .gentoo:   return [v("current", as: "Latest")]
        case .kali:     return [v("current", as: "Latest")]
        case .nixos: return [v("current", as: "Latest")]
        case .openeuler: return [v("20.03"), v("22.03"), v("23.03")]
        case .opensuse: return [v("tumbleweed", as: "Tumbleweed"), v("15.4"), v("15.5")]
        case .oracle: return [v("8"), v("9")]
        case .rocky:  return [v("8"), v("9")]
        case .ubuntu:   return [
            //v("xenial", as: "16.04 LTS (Xenial Xerus)"),
            v("bionic", as: "18.04 LTS (Bionic Beaver)"),
            v("focal", as: "20.04 LTS (Focal Fossa)"),
            v("jammy", as: "22.04 LTS (Jammy Jellyfish)"),
            //v("mantic", as: "23.10 (Mantic Minotaur, future)"),
            v("lunar", as: "23.04 (Lunar Lobster)")
        ]
        case .void:     return [v("current", as: "Latest")]
        }
    }
}
