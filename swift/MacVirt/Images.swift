//
// Created by Danny Lin on 2/5/23.
//

import Foundation

enum ImageKey: String {
    case alpine   = "alpine"
    case arch     = "archlinux"
    case centos   = "centos"
    case debian   = "debian"
    case fedora   = "fedora"
    case gentoo   = "gentoo"
    case kali     = "kali"
    case opensuse = "opensuse"
    case ubuntu   = "ubuntu"
    case void     = "voidlinux"

    case devuan = "devuan"
    case alma   = "almalinux"
    //case amazon = "amazonlinux"
    case oracle = "oracle"
    case rocky  = "rockylinux"

    // extra
    case nixos  = "nixos"
    case docker = "docker" // can't be created
    //case ubuntuFull = "ubuntu-full" // not yet supported
}

enum Distro: String, CaseIterable {
    //case Amazon = "amazon"
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
    case opensuse = "opensuse"
    case oracle = "oracle"
    case rocky  = "rocky"
    case ubuntu   = "ubuntu"
    case void     = "void"

    var imageKey: ImageKey {
        switch self {
        case .alpine:   return .alpine
        case .arch:     return .arch
        case .centos:   return .centos
        case .debian:   return .debian
        case .fedora:   return .fedora
        case .gentoo:   return .gentoo
        case .kali:     return .kali
        case .opensuse: return .opensuse
        case .ubuntu:   return .ubuntu
        case .void:     return .void

        case .devuan: return .devuan
        case .alma:   return .alma
        //case .Amazon: return .Amazon
        case .oracle: return .oracle
        case .rocky:  return .rocky

        // extra
        case .nixos: return .nixos
        }
    }

    var friendlyName: String {
        switch self {
        case .alpine:   return "Alpine"
        case .arch:     return "Arch"
        case .centos:   return "CentOS"
        case .debian:   return "Debian"
        case .fedora:   return "Fedora"
        case .gentoo:   return "Gentoo"
        case .kali:     return "Kali"
        case .opensuse: return "OpenSUSE"
        case .ubuntu:   return "Ubuntu"
        case .void:     return "Void"

        case .devuan: return "Devuan"
        case .alma:   return "Alma"
        //case .Amazon: return "Amazon"
        case .oracle: return "Oracle"
        case .rocky:  return "Rocky"

        // extra
        case .nixos: return "NixOS"
        }
    }
}
