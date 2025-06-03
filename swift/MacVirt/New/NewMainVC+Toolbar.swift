//
//  NewMainVC+Toolbar.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/10/23.
//

import AppKit
import Defaults
import SwiftUI
import UniformTypeIdentifiers

extension NewMainViewController: NSToolbarDelegate, NSToolbarItemValidation {
    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        guard let toolbarIdentifier = NavTabId(rawValue: toolbar.identifier) else {
            NSLog("Allow - no matching toolbar identifier! \(toolbar.identifier)")
            return []
        }

        var items = [NSToolbarItem.Identifier]()
        items += NSToolbarItem.Identifier.leadingItems
        items += toolbarIdentifier.defaultItemIdentifiers
        items += NSToolbarItem.Identifier.trailingItemsStart
        items += toolbarIdentifier.trailingItemIdentifiers
        items += NSToolbarItem.Identifier.trailingItemsEnd
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
        items += NSToolbarItem.Identifier.trailingItemsStart
        items += toolbarIdentifier.trailingItemIdentifiers
        items += NSToolbarItem.Identifier.trailingItemsEnd

        return items
    }

    func toolbar(
        _: NSToolbar, itemForItemIdentifier itemIdentifier: NSToolbarItem.Identifier,
        willBeInsertedIntoToolbar _: Bool
    ) -> NSToolbarItem? {
        switch itemIdentifier {
        case .toggleInspectorButton:
            return toggleInspectorButton

        case .dockerContainersOpen:
            return containersFolderButton
        case .dockerContainersSort:
            return containersSortMenu
        case .dockerContainersFilter:
            return containersFilterMenu

        case .dockerVolumesOpen:
            return volumesFolderButton
        case .dockerVolumesImport:
            return volumesImportButton
        case .dockerVolumesNew:
            return volumesPlusButton
        case .dockerVolumesSort:
            return volumesSortMenu

        case .dockerImagesOpen:
            return imagesFolderButton
        case .dockerImagesSort:
            return imagesSortMenu
        case .dockerImagesImport:
            return imagesImportButton

        case .k8sEnable:
            return podsStartToggle
        case .k8sPodsFilter:
            return podsFilterMenu
        case .k8sServicesFilter:
            return servicesFilterMenu

        case .machinesOpen:
            return machinesFolderButton
        case .machinesImport:
            return machinesImportButton
        case .machinesNew:
            return machinesPlusButton

        case .cliHelp:
            return commandsHelpButton

        case .activityMonitorStop:
            return activityMonitorStopButton

        case .searchItem:
            return searchItem
        case .inspectorTrackingSeparatorCompat:
            return NSTrackingSeparatorToolbarItem(
                identifier: itemIdentifier, splitView: splitViewController.splitView,
                dividerIndex: 1)

        case .licenseBadge:
            return licenseBadgeItem

        default:
            break
        }

        // non-custom, system toolbar items
        return NSToolbarItem(itemIdentifier: itemIdentifier)
    }

    func validateToolbarItem(_ item: NSToolbarItem) -> Bool {
        return item.isEnabled
    }

    @objc func actionToggleInspector(_: NSButton?) {
        splitViewController.itemC.animator().isCollapsed.toggle()
    }

    @objc func actionDockerContainersFilter1(_: Any?) {
        Defaults[.dockerFilterShowStopped].toggle()
    }

    @objc func actionDockerContainersOpen(_: NSButton?) {
        NSWorkspace.openFolder(Folders.nfsDockerContainers)
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
        Defaults[.k8sFilterShowSystemNs].toggle()
    }

    @objc func actionK8sServicesFilter1(_: Any?) {
        Defaults[.k8sFilterShowSystemNs].toggle()
    }

    @objc func actionMachinesNew(_: NSButton?) {
        model.presentCreateMachine = true
    }

    @objc func actionMachinesOpen(_: NSButton?) {
        NSWorkspace.openFolder(Folders.nfs)
    }

    @objc func actionMachinesImport(_ toolbarItem: NSToolbarItem?) {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        // ideally we can filter for .tar.zst but that's not possible :(
        panel.allowedContentTypes = [UTType(filenameExtension: "zst", conformingTo: .data)!]
        panel.canChooseDirectories = false
        panel.canCreateDirectories = false
        panel.message = "Select machine (.tar.zst) to import"

        let window = toolbarItem?.view?.window ?? NSApp.keyWindow ?? NSApp.windows.first!
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
                let url = panel.url
            {
                self.model.presentImportMachine = url
            }
        }
    }

    @objc func actionDockerVolumesImport(_ toolbarItem: NSToolbarItem?) {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        // ideally we can filter for .tar.zst but that's not possible :(
        panel.allowedContentTypes = [UTType(filenameExtension: "zst", conformingTo: .data)!]
        panel.canChooseDirectories = false
        panel.canCreateDirectories = false
        panel.message = "Select volume (.tar.zst) to import"

        let window = toolbarItem?.view?.window ?? NSApp.keyWindow ?? NSApp.windows.first!
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
                let url = panel.url
            {
                self.model.presentImportVolume = url
            }
        }
    }

    @objc func actionDockerImagesImport(_ toolbarItem: NSToolbarItem?) {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.allowedContentTypes = [UTType(filenameExtension: "tar", conformingTo: .data)!]
        panel.canChooseDirectories = false
        panel.canCreateDirectories = false
        panel.message = "Select image (.tar) to import"

        let window = toolbarItem?.view?.window ?? NSApp.keyWindow ?? NSApp.windows.first!
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
                let url = panel.url
            {
                self.model.dockerImageImportRouter.send(url)
            }
        }
    }

    @objc func actionCliHelp(_: NSButton?) {
        NSWorkspace.shared.open(URL(string: "https://orb.cx/cli")!)
    }

    @objc func actionActivityMonitorStop(_: NSButton?) {
        model.toolbarActionRouter.send(.activityMonitorStop)
    }
}
