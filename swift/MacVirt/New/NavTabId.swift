//
//  NavTabId.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/10/23.
//

import SwiftUI

enum NavTabId: String {
    case dockerContainers = "docker"
    case dockerVolumes = "docker-volumes"
    case dockerImages = "docker-images"

    case k8sPods = "k8s-pods"
    case k8sServices = "k8s-services"

    case machines

    case cli

    var defaultItemIdentifiers: [NSToolbarItem.Identifier] {
        switch self {
        case .dockerContainers:
            return [.dockerContainersFilter, .searchItem]
        case .dockerVolumes:
            return [.dockerVolumesOpen, .dockerVolumesNew, .searchItem]
        case .dockerImages:
            return [.dockerImagesOpen, .searchItem]

        case .k8sPods:
            return [.k8sEnable, .k8sPodsFilter, .searchItem]
        case .k8sServices:
            return [.k8sServicesFilter, .searchItem]

        case .machines:
            return [.machinesNew]

        case .cli:
            return [.cliHelp]
        }
    }
}

extension NSToolbarItem.Identifier {
    // use custom buttons for more flexibility
    static let toggleSidebarButton = NSToolbarItem.Identifier(rawValue: "toggleSidebarButton")
    static let toggleInspectorButton = NSToolbarItem.Identifier(rawValue: "toggleInspectorButton")

    static let dockerContainersFilter = NSToolbarItem.Identifier(rawValue: "dockerContainersFilter")
    static let dockerVolumesOpen = NSToolbarItem.Identifier(rawValue: "dockerVolumesOpen")
    static let dockerVolumesNew = NSToolbarItem.Identifier(rawValue: "dockerVolumesNew")
    static let dockerImagesOpen = NSToolbarItem.Identifier(rawValue: "dockerImagesOpen")

    static let k8sEnable = NSToolbarItem.Identifier(rawValue: "k8sEnable")
    static let k8sPodsFilter = NSToolbarItem.Identifier(rawValue: "k8sPodsFilter")
    static let k8sServicesFilter = NSToolbarItem.Identifier(rawValue: "k8sServicesFilter")

    static let machinesNew = NSToolbarItem.Identifier(rawValue: "machinesNew")

    static let cliHelp = NSToolbarItem.Identifier(rawValue: "cliHelp")

    static let searchItem = NSToolbarItem.Identifier("searchItem")

    static let leadingItems: [NSToolbarItem.Identifier] = [.toggleSidebarButton, .sidebarTrackingSeparator]

    static let trailingItems: [NSToolbarItem.Identifier] = {
        if #available(macOS 14.0, *) {
            [.inspectorTrackingSeparator, .flexibleSpace, .toggleInspectorButton]
        } else {
            [.toggleInspectorButton]
        }
    }()
}
