//
//  NavTabId.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/10/23.
//

import SwiftUI

extension NavTabId {
    var defaultItemIdentifiers: [NSToolbarItem.Identifier] {
        switch self {
        case .dockerContainers:
            return [ /*.dockerContainersSort,*/.dockerContainersFilter, .searchItem]
        case .dockerVolumes:
            return [
                .dockerVolumesSort, .dockerVolumesImport, .dockerVolumesNew,
                .searchItem,
            ]
        case .dockerImages:
            return [.dockerImagesSort, .dockerImagesImport, .searchItem]
        case .dockerNetworks:
            return [.dockerNetworksSort, .dockerNetworksNew, .searchItem]

        case .k8sPods:
            return [.k8sEnable, .k8sPodsFilter, .searchItem]
        case .k8sServices:
            return [.k8sServicesFilter, .searchItem]

        case .machines:
            return [.machinesImport, .machinesNew, .searchItem]

        case .cli:
            return [.cliHelp]

        case .activityMonitor:
            return [.activityMonitorStop]
        }
    }

    var trailingItemIdentifiers: [NSToolbarItem.Identifier] {
        switch self {
        case .dockerContainers:
            return [.dockerContainersOpen]
        case .dockerVolumes:
            return [.dockerVolumesOpen]
        case .dockerImages:
            return [.dockerImagesOpen]

        case .machines:
            return [.machinesOpen]

        default:
            return []
        }
    }
}

extension NSToolbarItem.Identifier {
    // use custom buttons for more flexibility
    static let toggleInspectorButton = NSToolbarItem.Identifier("toggleInspectorButton")

    static let dockerContainersOpen = NSToolbarItem.Identifier("dockerContainersOpen")
    static let dockerContainersSort = NSToolbarItem.Identifier("dockerContainersSort")
    static let dockerContainersFilter = NSToolbarItem.Identifier("dockerContainersFilter")

    static let dockerVolumesOpen = NSToolbarItem.Identifier("dockerVolumesOpen")
    static let dockerVolumesNew = NSToolbarItem.Identifier("dockerVolumesNew")
    static let dockerVolumesImport = NSToolbarItem.Identifier("dockerVolumesImport")
    static let dockerVolumesSort = NSToolbarItem.Identifier("dockerVolumesSort")

    static let dockerImagesOpen = NSToolbarItem.Identifier("dockerImagesOpen")
    static let dockerImagesSort = NSToolbarItem.Identifier("dockerImagesSort")
    static let dockerImagesImport = NSToolbarItem.Identifier("dockerImagesImport")

    static let dockerNetworksNew = NSToolbarItem.Identifier("dockerNetworksNew")
    static let dockerNetworksSort = NSToolbarItem.Identifier("dockerNetworksSort")

    static let k8sEnable = NSToolbarItem.Identifier("k8sEnable")
    static let k8sPodsFilter = NSToolbarItem.Identifier("k8sPodsFilter")
    static let k8sServicesFilter = NSToolbarItem.Identifier("k8sServicesFilter")

    static let machinesOpen = NSToolbarItem.Identifier("machinesOpen")
    static let machinesImport = NSToolbarItem.Identifier("machinesImport")
    static let machinesNew = NSToolbarItem.Identifier("machinesNew")

    static let cliHelp = NSToolbarItem.Identifier("cliHelp")

    static let activityMonitorStop = NSToolbarItem.Identifier("activityMonitorStop")

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
    static let trailingItemsStart: [NSToolbarItem.Identifier] =
        [.inspectorTrackingSeparatorCompat, .licenseBadge, .flexibleSpace]
    static let trailingItemsEnd: [NSToolbarItem.Identifier] =
        // macOS 14 .toggleInspector starts in disabled state, until a selection is made. custom one works
        [.space, .toggleInspectorButton]
}
