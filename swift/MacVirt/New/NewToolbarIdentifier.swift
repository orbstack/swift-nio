//
//  NewToolbarIdentifiers.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/10/23.
//

import SwiftUI

enum NewToolbarIdentifier: String {
    case containers = "docker"
    case volumes = "docker-volumes"
    case images = "docker-images"
    case pods = "k8s-pods"
    case services = "k8s-services"
    case machines = "machines"
    case commands = "cli"

    var defaultItemIdentifiers: [NSToolbarItem.Identifier] {
        switch self {
        case .containers:
            return [.containersFilterMenu, .searchItem]
        case .volumes:
            return [.volumesFolderButton, .volumesPlusButton, .searchItem]
        case .images:
            return [.imagesFolderButton, .searchItem]
        case .pods:
            return [.podsStartToggle, .podsFilterMenu, .searchItem]
        case .services:
            return [.servicesFilterMenu, .searchItem]
        case .machines:
            return [.machinesPlusButton]
        case .commands:
            return [.commandsHelpButton]
        }
    }
}

extension NSToolbarItem.Identifier {
    
    // use custom buttons for more flexibility
    static let toggleSidebarButton = NSToolbarItem.Identifier(rawValue: "toggleSidebarButton")
    static let toggleInspectorButton = NSToolbarItem.Identifier(rawValue: "toggleInspectorButton")
    
    static let containersFilterMenu = NSToolbarItem.Identifier(rawValue: "containersFilterMenu")
    static let volumesFolderButton = NSToolbarItem.Identifier(rawValue: "volumesFolderButton")
    static let volumesPlusButton = NSToolbarItem.Identifier(rawValue: "volumesPlusButton")
    static let imagesFolderButton = NSToolbarItem.Identifier(rawValue: "imagesFolderButton")
    static let podsStartToggle = NSToolbarItem.Identifier(rawValue: "podsStartToggle")
    static let podsFilterMenu = NSToolbarItem.Identifier(rawValue: "podsFilterMenu")
    static let servicesFilterMenu = NSToolbarItem.Identifier(rawValue: "servicesFilterMenu")
    static let machinesPlusButton = NSToolbarItem.Identifier(rawValue: "machinesPlusButton")
    static let commandsHelpButton = NSToolbarItem.Identifier(rawValue: "commandsHelpButton")

    static let searchItem = NSToolbarItem.Identifier("searchItem")

    static let leadingItems: [NSToolbarItem.Identifier] = {
        if #available(macOS 14.0, *) {
            return [.toggleSidebarButton, .sidebarTrackingSeparator]
        } else {
            return [.toggleSidebarButton, .sidebarTrackingSeparator]
        }
    }()

    static let trailingItems: [NSToolbarItem.Identifier] = {
        if #available(macOS 14.0, *) {
            return [.inspectorTrackingSeparator, .flexibleSpace, .toggleInspectorButton]
        } else {
            return [.toggleInspectorButton]
        }
    }()
}
