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
        case .dockerContainersOpenWindow:
            return containersOpenWindowButton
        case .dockerContainersSort:
            return containersSortMenu
        case .dockerContainersFilter:
            return containersFilterMenu
        case .dockerContainersNew:
            return containersPlusButton
        case .dockerContainersTabs:
            return containersTabs

        case .dockerVolumesOpen:
            return volumesFolderButton
        case .dockerVolumesImport:
            return volumesImportButton
        case .dockerVolumesNew:
            return volumesPlusButton
        case .dockerVolumesSort:
            return volumesSortMenu
        case .dockerVolumesTabs:
            return volumesTabs

        case .dockerImagesOpen:
            return imagesFolderButton
        case .dockerImagesSort:
            return imagesSortMenu
        case .dockerImagesImport:
            return imagesImportButton
        case .dockerImagesTabs:
            return imagesTabs

        case .dockerNetworksNew:
            return networksPlusButton
        case .dockerNetworksSort:
            return networksSortMenu
        case .dockerNetworksTabs:
            return networksTabs

        case .k8sEnable:
            return podsStartToggle
        case .k8sPodsFilter:
            return podsFilterMenu
        case .k8sPodsTabs:
            return podsTabs

        case .k8sServicesFilter:
            return servicesFilterMenu
        case .k8sServicesTabs:
            return servicesTabs

        case .machinesOpen:
            return machinesFolderButton
        case .machinesImport:
            return machinesImportButton
        case .machinesNew:
            return machinesPlusButton
        case .machinesOpenInNewWindow:
            return machinesOpenInNewWindowButton
        case .machinesTabs:
            return machinesTabs

        case .cliHelp:
            return commandsHelpButton

        case .activityMonitorStop:
            return activityMonitorStopButton

        case .searchItem:
            return searchItem

        case .licenseBadge:
            return licenseBadgeItem

        case .contentListTrackingSeparator:
            return NSTrackingSeparatorToolbarItem(identifier: .contentListTrackingSeparator, splitView: splitViewController.splitView, dividerIndex: 1)

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

    @objc func actionDockerContainersNew(_: NSButton?) {
        model.presentCreateContainer = true
    }

    @objc func actionDockerContainersOpen(_: NSButton?) {
        NSWorkspace.openFolder(Folders.nfsDockerContainers)
    }

    @objc func actionDockerContainersOpenWindow(_: NSButton?) {
        model.toolbarActionRouter.send(.dockerOpenContainerInNewWindow)
    }

    @objc func actionDockerContainersTabs(_ sender: NSToolbarItemGroup) {
        model.containerTab = ContainerTabId.allCases[sender.selectedIndex]
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

    @objc func actionDockerImagesOpenWindow(_: NSButton?) {
        model.toolbarActionRouter.send(.dockerOpenImageInNewWindow)
    }

    @objc func actionDockerNetworksNew(_: NSButton?) {
        model.presentCreateNetwork = true
    }

    @objc func actionDockerVolumesOpenWindow(_: NSButton?) {
        model.toolbarActionRouter.send(.dockerOpenVolumeInNewWindow)
    }

    @objc func actionK8sToggle(_ sender: NSSwitch?) {
        guard let sender else { return }
        Task { @MainActor in
            await model.tryStartStopK8s(enable: sender.state == .on)
        }
    }

    @objc func actionK8sPodsOpenWindow(_: NSButton?) {
        model.toolbarActionRouter.send(.k8sPodOpenInNewWindow)
    }

    @objc func actionK8sPodsFilter1(_: Any?) {
        Defaults[.k8sFilterShowSystemNs].toggle()
    }

    @objc func actionK8sServicesOpenWindow(_: NSButton?) {
        model.toolbarActionRouter.send(.k8sServiceOpenInNewWindow)
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

    @objc func actionMachinesOpenInNewWindow(_ toolbarItem: NSToolbarItem?) {
        model.toolbarActionRouter.send(.machineOpenInNewWindow)
    }

    @objc func actionMachinesTabs(_ sender: NSToolbarItemGroup) {
        model.machineTab = MachineTabId.allCases[sender.selectedIndex]
    }

    @objc func actionDockerVolumesTabs(_ sender: NSToolbarItemGroup) {
        model.volumesTab = VolumeTabId.allCases[sender.selectedIndex]
    }

    @objc func actionDockerImagesTabs(_ sender: NSToolbarItemGroup) {
        model.imagesTab = ImageTabId.allCases[sender.selectedIndex]
    }

    @objc func actionDockerNetworksTabs(_ sender: NSToolbarItemGroup) {
        model.networksTab = NetworkTabId.allCases[sender.selectedIndex]
    }

    @objc func actionK8sPodsTabs(_ sender: NSToolbarItemGroup) {
        model.podsTab = PodsTabId.allCases[sender.selectedIndex]
    }

    @objc func actionK8sServicesTabs(_ sender: NSToolbarItemGroup) {
        model.servicesTab = ServicesTabId.allCases[sender.selectedIndex]
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
