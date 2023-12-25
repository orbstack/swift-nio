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
        guard let toolbarIdentifier = NewToolbarIdentifier(rawValue: toolbar.identifier) else {
            print("Allow - no matching toolbar identifier! \(toolbar.identifier)")
            return []
        }

        var items = [NSToolbarItem.Identifier]()
        items += NSToolbarItem.Identifier.leadingItems
        items += toolbarIdentifier.defaultItemIdentifiers
        items += NSToolbarItem.Identifier.trailingItems
        return items
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        guard let toolbarIdentifier = NewToolbarIdentifier(rawValue: toolbar.identifier) else {
            print("Default - no matching toolbar identifier! \(toolbar.identifier)")
            return []
        }

        var items = [NSToolbarItem.Identifier]()
        items += NSToolbarItem.Identifier.leadingItems
        items += toolbarIdentifier.defaultItemIdentifiers
        items += NSToolbarItem.Identifier.trailingItems

        return items
    }

    func toolbar(_ toolbar: NSToolbar, itemForItemIdentifier itemIdentifier: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool) -> NSToolbarItem? {
        switch itemIdentifier {
        case .toggleSidebarButton:
            return toggleSidebarButton
        case .toggleInspectorButton:
            return toggleInspectorButton
        case .containersFilterMenu:
            return containersFilterMenu
        case .volumesFolderButton:
            return volumesFolderButton
        case .volumesPlusButton:
            return volumesPlusButton
        case .imagesFolderButton:
            return imagesFolderButton
        case .podsStartToggle:
            return podsStartToggle
        case .podsFilterMenu:
            return podsFilterMenu
        case .servicesFilterMenu:
            return servicesFilterMenu
        case .machinesPlusButton:
            return machinesPlusButton
        case .commandsHelpButton:
            return commandsHelpButton
        case .searchItem:
            return searchItem
        default:
            break
        }

        // non-custom, system toolbar items
        return NSToolbarItem(itemIdentifier: itemIdentifier)
    }

    @objc func toggleSidebarButton(_ sender: NSButton?) {
        splitViewController.itemA.animator().isCollapsed.toggle()
    }

    @objc func toggleInspectorButton(_ sender: NSButton?) {
        splitViewController.itemC.animator().isCollapsed.toggle()
    }

    @objc func containersFilterMenu1(_ sender: Any?) {
        model.dockerFilterShowStopped.toggle()
    }

    @objc func volumesFolderButton(_ sender: NSButton?) {
        NSWorkspace.openFolder(Folders.nfsDockerVolumes)
    }

    @objc func volumesPlusButton(_ sender: NSButton?) {
        model.presentCreateVolume = true
    }

    @objc func imagesFolderButton(_ sender: NSButton?) {
        NSWorkspace.openFolder(Folders.nfsDockerImages)
    }

    @objc func podsStartToggle(_ sender: NSSwitch?) {
        guard let sender else { return }
        Task { @MainActor in
            await model.tryStartStopK8s(enable: sender.state == .on)
        }
    }

    @objc func podsFilterMenu1(_ sender: Any?) {
        model.k8sFilterShowSystemNs.toggle()
    }

    @objc func servicesFilterMenu1(_ sender: Any?) {
        model.k8sFilterShowSystemNs.toggle()
    }

    @objc func machinesPlusButton(_ sender: NSButton?) {
        model.presentCreateMachine = true
    }

    @objc func commandsHelpButton(_ sender: NSButton?) {
        NSWorkspace.shared.open(URL(string: "https://go.orbstack.dev/cli")!)
    }
}
