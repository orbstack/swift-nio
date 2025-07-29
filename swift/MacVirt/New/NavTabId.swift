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
            return [ /*.dockerContainersSort,*/
                .dockerContainersFilter, .searchItem,
            ]
        case .dockerVolumes:
            return [
                .dockerVolumesSort, .searchItem,
            ]
        case .dockerImages:
            return [.dockerImagesSort, .searchItem]
        case .dockerNetworks:
            return [.dockerNetworksSort, .searchItem]

        case .k8sPods:
            return [.k8sEnable, .k8sPodsFilter, .searchItem]
        case .k8sServices:
            return [.k8sServicesFilter, .searchItem]

        case .machines:
            return [.searchItem]

        case .cli:
            return [.cliHelp]

        case .activityMonitor:
            return [.activityMonitorStop]
        }
    }

    var trailingItemIdentifiers: [NSToolbarItem.Identifier] {
        switch self {
        case .dockerContainers:
            return [
                .dockerContainersNew, .flexibleSpace, .dockerContainersTabs, .flexibleSpace,
                .licenseBadge, .dockerContainersOpenWindow,
            ]
        case .dockerVolumes:
            return [
                .dockerVolumesNew, .flexibleSpace, .dockerVolumesTabs, .flexibleSpace,
                .licenseBadge, .dockerVolumesOpenWindow,
            ]
        case .dockerImages:
            return [
                .dockerImagesImport, .flexibleSpace, .dockerImagesTabs, .flexibleSpace,
                .licenseBadge, .dockerImagesOpenWindow,
            ]
        case .dockerNetworks:
            return [
                .dockerNetworksNew, .flexibleSpace, .dockerNetworksTabs, .flexibleSpace,
                .licenseBadge, .dockerNetworksOpenWindow,
            ]

        case .k8sPods:
            return [
                .k8sEnable, .flexibleSpace, .k8sPodsTabs, .flexibleSpace, .licenseBadge,
                .k8sPodsOpenWindow,
            ]
        case .k8sServices:
            return [
                .k8sServicesFilter, .flexibleSpace, .k8sServicesTabs, .flexibleSpace, .licenseBadge,
                .k8sServicesOpenWindow,
            ]

        case .machines:
            return [
                .machinesNew, .flexibleSpace, .machinesTabs, .flexibleSpace, .licenseBadge,
                .machinesOpenInNewWindow,
            ]

        default:
            return [.flexibleSpace, .licenseBadge]
        }
    }
}

enum ContainerTabId: CaseIterable, CustomStringConvertible {
    case info
    case logs
    case terminal
    case files

    var description: String {
        switch self {
        case .info:
            return "Info"
        case .logs:
            return "Logs"
        case .terminal:
            return "Terminal"
        case .files:
            return "Files"
        }
    }
}

enum MachineTabId: CaseIterable, CustomStringConvertible {
    case info
    case logs
    case terminal
    case files

    var description: String {
        switch self {
        case .info:
            return "Info"
        case .logs:
            return "Logs"
        case .terminal:
            return "Terminal"
        case .files:
            return "Files"
        }
    }
}

enum VolumeTabId: CaseIterable, CustomStringConvertible {
    case info
    case files

    var description: String {
        switch self {
        case .info:
            return "Info"
        case .files:
            return "Files"
        }
    }
}

enum ImageTabId: CaseIterable, CustomStringConvertible {
    case info
    case terminal
    case files

    var description: String {
        switch self {
        case .info:
            return "Info"
        case .terminal:
            return "Terminal"
        case .files:
            return "Files"
        }
    }
}

enum NetworkTabId: CaseIterable, CustomStringConvertible {
    case info

    var description: String {
        switch self {
        case .info:
            return "Info"
        }
    }
}

enum PodsTabId: CaseIterable, CustomStringConvertible {
    case info
    case logs
    case terminal

    var description: String {
        switch self {
        case .info:
            return "Info"
        case .logs:
            return "Logs"
        case .terminal:
            return "Terminal"
        }
    }
}

enum ServicesTabId: CaseIterable, CustomStringConvertible {
    case info

    var description: String {
        switch self {
        case .info:
            return "Info"
        }
    }
}

extension NSToolbarItem.Identifier {
    // use custom buttons for more flexibility
    static let toggleInspectorButton = NSToolbarItem.Identifier("toggleInspectorButton")

    static let dockerContainersOpen = NSToolbarItem.Identifier("dockerContainersOpen")
    static let dockerContainersOpenWindow = NSToolbarItem.Identifier("dockerContainersOpenWindow")
    static let dockerContainersSort = NSToolbarItem.Identifier("dockerContainersSort")
    static let dockerContainersFilter = NSToolbarItem.Identifier("dockerContainersFilter")
    static let dockerContainersNew = NSToolbarItem.Identifier("dockerContainersNew")
    static let dockerContainersTabs = NSToolbarItem.Identifier("dockerContainersTabs")

    static let dockerVolumesOpen = NSToolbarItem.Identifier("dockerVolumesOpen")
    static let dockerVolumesNew = NSToolbarItem.Identifier("dockerVolumesNew")
    static let dockerVolumesImport = NSToolbarItem.Identifier("dockerVolumesImport")
    static let dockerVolumesSort = NSToolbarItem.Identifier("dockerVolumesSort")
    static let dockerVolumesTabs = NSToolbarItem.Identifier("dockerVolumesTabs")
    static let dockerVolumesOpenWindow = NSToolbarItem.Identifier("dockerVolumesOpenWindow")

    static let dockerImagesOpen = NSToolbarItem.Identifier("dockerImagesOpen")
    static let dockerImagesSort = NSToolbarItem.Identifier("dockerImagesSort")
    static let dockerImagesImport = NSToolbarItem.Identifier("dockerImagesImport")
    static let dockerImagesTabs = NSToolbarItem.Identifier("dockerImagesTabs")
    static let dockerImagesOpenWindow = NSToolbarItem.Identifier("dockerImagesOpenWindow")

    static let dockerNetworksNew = NSToolbarItem.Identifier("dockerNetworksNew")
    static let dockerNetworksSort = NSToolbarItem.Identifier("dockerNetworksSort")
    static let dockerNetworksTabs = NSToolbarItem.Identifier("dockerNetworksTabs")
    static let dockerNetworksOpenWindow = NSToolbarItem.Identifier("dockerNetworksOpenWindow")

    static let k8sEnable = NSToolbarItem.Identifier("k8sEnable")
    static let k8sPodsFilter = NSToolbarItem.Identifier("k8sPodsFilter")
    static let k8sPodsTabs = NSToolbarItem.Identifier("k8sPodsTabs")
    static let k8sPodsOpenWindow = NSToolbarItem.Identifier("k8sPodsOpenWindow")

    static let k8sServicesFilter = NSToolbarItem.Identifier("k8sServicesFilter")
    static let k8sServicesTabs = NSToolbarItem.Identifier("k8sServicesTabs")
    static let k8sServicesOpenWindow = NSToolbarItem.Identifier("k8sServicesOpenWindow")

    static let machinesOpen = NSToolbarItem.Identifier("machinesOpen")
    static let machinesImport = NSToolbarItem.Identifier("machinesImport")
    static let machinesNew = NSToolbarItem.Identifier("machinesNew")
    static let machinesOpenInNewWindow = NSToolbarItem.Identifier("machinesOpenInNewWindow")
    static let machinesTabs = NSToolbarItem.Identifier("machinesTabs")

    static let cliHelp = NSToolbarItem.Identifier("cliHelp")

    static let activityMonitorStop = NSToolbarItem.Identifier("activityMonitorStop")

    static let searchItem = NSToolbarItem.Identifier("searchItem")

    static let licenseBadge = NSToolbarItem.Identifier("licenseBadge")

    static let contentListTrackingSeparator = NSToolbarItem.Identifier(
        "contentListTrackingSeparator")

    static let leadingItems: [NSToolbarItem.Identifier] =
        [.toggleSidebar, .sidebarTrackingSeparator]
    static let trailingItemsStart: [NSToolbarItem.Identifier] =
        [.contentListTrackingSeparator]
    static let trailingItemsEnd: [NSToolbarItem.Identifier] =
        // macOS 14 .toggleInspector starts in disabled state, until a selection is made. custom one works
        []
}
