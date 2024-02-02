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
    static let toggleInspectorButton = NSToolbarItem.Identifier("toggleInspectorButton")

    static let dockerContainersFilter = NSToolbarItem.Identifier("dockerContainersFilter")
    static let dockerVolumesOpen = NSToolbarItem.Identifier("dockerVolumesOpen")
    static let dockerVolumesNew = NSToolbarItem.Identifier("dockerVolumesNew")
    static let dockerImagesOpen = NSToolbarItem.Identifier("dockerImagesOpen")

    static let k8sEnable = NSToolbarItem.Identifier("k8sEnable")
    static let k8sPodsFilter = NSToolbarItem.Identifier("k8sPodsFilter")
    static let k8sServicesFilter = NSToolbarItem.Identifier("k8sServicesFilter")

    static let machinesNew = NSToolbarItem.Identifier("machinesNew")

    static let cliHelp = NSToolbarItem.Identifier("cliHelp")

    static let searchItem = NSToolbarItem.Identifier("searchItem")

    static let licenseBadge = NSToolbarItem.Identifier("licenseBadge")

    static let inspectorTrackingSeparatorCompat = {
        if #available(macOS 14.0, *) {
            NSToolbarItem.Identifier.inspectorTrackingSeparator
        } else {
            NSToolbarItem.Identifier("inspectorTrackingSeparatorCompat")
        }
    }()

    static let leadingItems: [NSToolbarItem.Identifier] =
        [.toggleSidebar, .sidebarTrackingSeparator]
    static let trailingItems: [NSToolbarItem.Identifier] =
        // macOS 14 .toggleInspector starts in disabled state, until a selection is made. custom one works
        [.inspectorTrackingSeparatorCompat, .flexibleSpace, .licenseBadge, .toggleInspectorButton]
}
