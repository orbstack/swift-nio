//
//  NewMainVC+Toolbar.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/10/23.
//

import AppKit
import Defaults
import SwiftUI

extension NewMainViewController: NSToolbarDelegate {
    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        guard let toolbarIdentifier = NavTabId(rawValue: toolbar.identifier) else {
            NSLog("Allow - no matching toolbar identifier! \(toolbar.identifier)")
            return []
        }

        var items = [NSToolbarItem.Identifier]()
        items += NSToolbarItem.Identifier.leadingItems
        items += toolbarIdentifier.defaultItemIdentifiers
        items += NSToolbarItem.Identifier.trailingItems
        return items
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        guard let toolbarIdentifier = NavTabId(rawValue: toolbar.identifier) else {
            NSLog("Default - no matching toolbar identifier! \(toolbar.identifier)")
            return []
        }

        var items = [NSToolbarItem.Identifier]()
        items += NSToolbarItem.Identifier.leadingItems
        items += toolbarIdentifier.defaultItemIdentifiers
        items += NSToolbarItem.Identifier.trailingItems

        return items
    }

    func toolbar(_: NSToolbar, itemForItemIdentifier itemIdentifier: NSToolbarItem.Identifier, willBeInsertedIntoToolbar _: Bool) -> NSToolbarItem? {
        switch itemIdentifier {
        case .toggleInspectorButton:
            return toggleInspectorButton

        case .dockerContainersFilter:
            return containersFilterMenu
        case .dockerVolumesOpen:
            return volumesFolderButton
        case .dockerVolumesNew:
            return volumesPlusButton
        case .dockerImagesOpen:
            return imagesFolderButton

        case .k8sEnable:
            return podsStartToggle
        case .k8sPodsFilter:
            return podsFilterMenu
        case .k8sServicesFilter:
            return servicesFilterMenu

        case .machinesNew:
            return machinesPlusButton

        case .cliHelp:
            return commandsHelpButton

        case .searchItem:
            return searchItem
        case .inspectorTrackingSeparatorCompat:
            return NSTrackingSeparatorToolbarItem(identifier: itemIdentifier, splitView: splitViewController.splitView, dividerIndex: 1)

        case .licenseBadge:
            return licenseBadgeItem

        default:
            break
        }

        // non-custom, system toolbar items
        return NSToolbarItem(itemIdentifier: itemIdentifier)
    }

    @objc func actionToggleInspector(_: NSButton?) {
        splitViewController.itemC.animator().isCollapsed.toggle()
    }

    @objc func actionDockerContainersFilter1(_: Any?) {
        model.dockerFilterShowStopped.toggle()
    }

    @objc func actionDockerVolumesOpen(_: NSButton?) {
        NSWorkspace.openFolder(Folders.nfsDockerVolumes)
    }

    @objc func actionDockerVolumesNew(_: NSButton?) {
        model.presentCreateVolume = true
    }

    @objc func actionDockerImagesOpen(_: NSButton?) {
        NSWorkspace.openFolder(Folders.nfsDockerImages)
    }

    @objc func actionK8sToggle(_ sender: NSSwitch?) {
        guard let sender else { return }
        Task { @MainActor in
            await model.tryStartStopK8s(enable: sender.state == .on)
        }
    }

    @objc func actionK8sPodsFilter1(_: Any?) {
        model.k8sFilterShowSystemNs.toggle()
    }

    @objc func actionK8sServicesFilter1(_: Any?) {
        model.k8sFilterShowSystemNs.toggle()
    }

    @objc func actionMachinesNew(_: NSButton?) {
        model.presentCreateMachine = true
    }

    @objc func actionCliHelp(_: NSButton?) {
        NSWorkspace.shared.open(URL(string: "https://go.orbstack.dev/cli")!)
    }
}
