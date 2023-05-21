//
// Created by Danny Lin on 5/20/23.
//

import Foundation
import Sparkle
import AppKit

private let maxPreviewContainers = 5

// must be @MainActor to access view model
@MainActor
class MenuBarController: NSObject, NSMenuDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    private let menu = NSMenu()
    private let updaterController: SPUStandardUpdaterController
    private let vmModel: VmViewModel

    init(updaterController: SPUStandardUpdaterController, vmModel: VmViewModel) {
        self.updaterController = updaterController
        self.vmModel = vmModel
        super.init()

        if let button = statusItem.button {
            // bold = larger, matches other menu bar icons
            // circle.hexagongrid.circle?
            button.image = systemImage("circle.circle.fill", bold: true)
        }
        statusItem.menu = menu
        menu.delegate = self

        updateMenu()
    }

    func menuWillOpen(_ menu: NSMenu) {
        updateMenu()
    }

    private func updateMenu() {
        menu.removeAllItems()

        // shortcut = cmd-enter
        let openItem = NSMenuItem(title: "Open OrbStack",
                action: #selector(actionOpenApp),
                keyEquivalent: "n")
        openItem.target = self
        openItem.image = systemImage("sidebar.leading")
        menu.addItem(openItem)

        menu.addItem(NSMenuItem.separator())

        // Docker containers
        if let dockerContainers = vmModel.dockerContainers {
            menu.addItem(makeSectionTitleItem(title: "Containers"))

            // only show running in menu bar
            let runningContainers = dockerContainers.filter { $0.running }
            // limit 5
            for container in runningContainers.prefix(maxPreviewContainers) {
                let item = makeContainerItem(container: container)
                menu.addItem(item)
            }

            // show extras in submenu
            if runningContainers.count > maxPreviewContainers {
                let submenu = NSMenu()
                let extraItem = NSMenuItem(title: "\(runningContainers.count - maxPreviewContainers) more",
                        action: nil,
                        keyEquivalent: "")
                extraItem.submenu = submenu
                menu.addItem(extraItem)

                // add system image icon to extraItem
                extraItem.image = systemImage("ellipsis")

                for container in runningContainers.dropFirst(maxPreviewContainers) {
                    let item = makeContainerItem(container: container)
                    submenu.addItem(item)
                }
            }

            // placeholder if no containers
            if runningContainers.isEmpty {
                let item = NSMenuItem()
                item.title = "None running"
                item.isEnabled = false
                menu.addItem(item)
            }

            menu.addItem(NSMenuItem.separator())
        }

        // Machines
        if let machines = vmModel.containers {
            menu.addItem(makeSectionTitleItem(title: "Machines"))

            // only show running in menu bar
            let runningMachines = machines.filter { $0.running }
            /*
            // limit 5
            for machine in runningMachines.prefix(maxPreviewContainers) {
                let item = makeContainerItem(container: machine)
                menu.addItem(item)
            }

            // show extras in submenu
            if runningMachines.count > maxPreviewContainers {
                let submenu = NSMenu()
                let extraItem = NSMenuItem(title: "\(runningMachines.count - maxPreviewContainers) more…",
                        action: nil,
                        keyEquivalent: "")
                extraItem.submenu = submenu
                menu.addItem(extraItem)

                // add system image icon to extraItem
                let icon = NSImage(systemSymbolName: "ellipsis", accessibilityDescription: nil)
                icon?.size = NSSize(width: 16, height: 16)
                extraItem.image = icon

                for machine in runningMachines.dropFirst(maxPreviewContainers) {
                    let item = makeContainerItem(container: machine)
                    submenu.addItem(item)
                }
            }*/

            // placeholder if no machines
            if runningMachines.isEmpty {
                let item = NSMenuItem()
                item.title = "None running"
                item.isEnabled = false
                menu.addItem(item)
            }

            menu.addItem(NSMenuItem.separator())
        }

        // check for updates
        let updateItem = NSMenuItem(title: "Check for Updates…",
                action: #selector(updaterController.checkForUpdates),
                keyEquivalent: "")
        updateItem.target = updaterController
        menu.addItem(updateItem)

        // settings
        let settingsItem = NSMenuItem(title: "Settings…",
                action: #selector(actionOpenSettings),
                keyEquivalent: ",")
        settingsItem.target = self
        menu.addItem(settingsItem)

        menu.addItem(NSMenuItem(title: "Quit",
                action: #selector(NSApplication.terminate),
                keyEquivalent: "q"))
    }

    private func makeContainerItem(container: DKContainer) -> NSMenuItem {
        let containerItem = NSMenuItem()
        containerItem.title = container.userName
        containerItem.target = self
        containerItem.action = #selector(actionShowContainerInfo)
        let controller = DockerContainerMenuItemController(container: container, vmModel: vmModel)
        containerItem.representedObject = controller

        let submenu = NSMenu()
        containerItem.submenu = submenu

        if container.running {
            let stopItem = NSMenuItem(title: "Stop",
                    action: #selector(controller.actionStop),
                    keyEquivalent: "")
            stopItem.target = controller
            stopItem.image = systemImage("stop.fill")
            submenu.addItem(stopItem)
        } else {
            let startItem = NSMenuItem(title: "Start",
                    action: #selector(controller.actionStart),
                    keyEquivalent: "")
            startItem.target = controller
            startItem.image = systemImage("play.fill")
            submenu.addItem(startItem)
        }

        let restartItem = NSMenuItem(title: "Restart",
                action: #selector(controller.actionRestart),
                keyEquivalent: "")
        restartItem.target = controller
        restartItem.image = systemImage("arrow.clockwise")
        restartItem.isEnabled = container.running
        submenu.addItem(restartItem)

        let deleteItem = NSMenuItem(title: "Delete",
                action: #selector(controller.actionDelete),
                keyEquivalent: "")
        deleteItem.target = controller
        deleteItem.image = systemImage("trash.fill")
        submenu.addItem(deleteItem)

        submenu.addItem(NSMenuItem.separator())

        let detailsItem = NSMenuItem(title: "Get Info",
                action: #selector(actionShowContainerInfo),
                keyEquivalent: "")
        detailsItem.target = self
        detailsItem.representedObject = container
        submenu.addItem(detailsItem)

        let logsItem = NSMenuItem(title: "Show Logs",
                action: #selector(controller.actionShowLogs),
                keyEquivalent: "")
        logsItem.target = controller
        submenu.addItem(logsItem)

        submenu.addItem(NSMenuItem.separator())

        let terminalItem = NSMenuItem(title: "Open Terminal",
                action: #selector(controller.actionOpenTerminal),
                keyEquivalent: "")
        terminalItem.target = controller
        submenu.addItem(terminalItem)

        submenu.addItem(NSMenuItem.separator())

        if !container.ports.isEmpty {
            submenu.addItem(makePortsItem(ports: container.ports))
        }
        if !container.mounts.isEmpty {
            submenu.addItem(makeMountsItem(mounts: container.mounts))
        }
        if container.ports.isEmpty && container.mounts.isEmpty {
            let item = NSMenuItem()
            item.title = "No Ports or Mounts"
            item.isEnabled = false
            submenu.addItem(item)
        }

        submenu.addItem(NSMenuItem.separator())

        submenu.addItem(makeCopyItem(container: container, controller: controller))

        return containerItem
    }

    private func makePortsItem(ports: [DKPort]) -> NSMenuItem {
        let portsItem = NSMenuItem()
        portsItem.title = "Ports"

        let submenu = NSMenu()
        portsItem.submenu = submenu

        for port in ports {
            let portController = DockerPortMenuItemController(port: port)

            let portItem = NSMenuItem(title: port.formatted,
                    action: #selector(portController.actionOpen),
                    keyEquivalent: "")
            portItem.target = portController
            // retain reference to prevent disabled
            portItem.representedObject = portController
            submenu.addItem(portItem)
        }

        return portsItem
    }

    private func makeMountsItem(mounts: [DKMountPoint]) -> NSMenuItem {
        let mountsItem = NSMenuItem()
        mountsItem.title = "Mounts"

        let submenu = NSMenu()
        mountsItem.submenu = submenu

        for mount in mounts {
            let mountController = DockerMountMenuItemController(mount: mount)

            let mountItem = NSMenuItem(title: mount.formatted,
                    action: #selector(mountController.actionOpen),
                    keyEquivalent: "")
            mountItem.target = mountController
            // retain reference to prevent disabled
            mountItem.representedObject = mountController
            submenu.addItem(mountItem)
        }

        return mountsItem
    }

    private func makeCopyItem(container: DKContainer, controller: DockerContainerMenuItemController) -> NSMenuItem {
        let copyItem = NSMenuItem()
        copyItem.title = "Copy"

        let submenu = NSMenu()
        copyItem.submenu = submenu

        let copyIDItem = NSMenuItem(title: "ID",
                action: #selector(actionCopyString),
                keyEquivalent: "")
        copyIDItem.target = self
        copyIDItem.representedObject = container.id
        submenu.addItem(copyIDItem)

        let copyNameItem = NSMenuItem(title: "Image",
                action: #selector(actionCopyString),
                keyEquivalent: "")
        copyNameItem.target = self
        copyNameItem.representedObject = container.image
        submenu.addItem(copyNameItem)

        let copyCommandItem = NSMenuItem(title: "Command",
                action: #selector(controller.actionCopyRunCommand),
                keyEquivalent: "")
        copyCommandItem.target = controller
        submenu.addItem(copyCommandItem)

        let copyIpItem = NSMenuItem(title: "IP",
                action: #selector(actionCopyString),
                keyEquivalent: "")
        let ipAddress = container.ipAddresses.first
        copyIpItem.target = self
        copyIpItem.representedObject = ipAddress ?? ""
        copyIpItem.isEnabled = ipAddress != nil
        submenu.addItem(copyIpItem)

        return copyItem
    }

    private func makeSectionTitleItem(title: String) -> NSMenuItem {
        let item = NSMenuItem()
        // use attributedTitle for emphasis
        item.attributedTitle = NSAttributedString(string: title, attributes: [
            NSAttributedString.Key.font: NSFont.systemFont(ofSize: 12, weight: .bold),
            NSAttributedString.Key.foregroundColor: NSColor.labelColor
        ])
        item.isEnabled = false
        return item
    }

    private func systemImage(_ name: String, bold: Bool = false) -> NSImage? {
        if let image = NSImage(systemSymbolName: name, accessibilityDescription: nil) {
            if bold {
                let config = NSImage.SymbolConfiguration(pointSize: 16, weight: .medium)
                return image.withSymbolConfiguration(config)
            } else {
                return image
            }
        }

        return nil
    }

    @objc private func actionOpenApp() {
        // open main window if needed, as if user clicked on dock
        // but always open main so users can get back to main, not e.g. logs
        if !NSApp.windows.contains(where: { $0.isUserFacing }) {
            NSApp.setActivationPolicy(.regular)
            NSWorkspace.shared.open(URL(string: "orbstack://main")!)
        }
        NSApp.activate(ignoringOtherApps: true)
    }

    @objc private func actionOpenSettings() {
        if #available(macOS 13, *) {
            NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
        } else {
            NSApp.sendAction(Selector(("showPreferencesWindow:")), to: nil, from: nil)
        }

        // then focus app
        NSApp.activate(ignoringOtherApps: true)
    }

    @objc private func actionShowContainerInfo(_ sender: NSMenuItem) {
        // TODO
        if let container = sender.representedObject as? DKContainer {
            NSApp.sendAction(Selector(("showContainerWindow:")), to: nil, from: container)
        }
    }

    @objc private func actionCopyString(_ sender: NSMenuItem) {
        if let string = sender.representedObject as? String {
            NSPasteboard.copy(string)
        }
    }
}

private class DockerContainerMenuItemController: NSObject {
    private let container: DKContainer
    private let vmModel: VmViewModel

    init(container: DKContainer, vmModel: VmViewModel) {
        self.container = container
        self.vmModel = vmModel
        super.init()
    }

    @objc func actionStart(_ sender: NSMenuItem) {
        Task { @MainActor in
            await vmModel.tryDockerContainerStart(container.id)
        }
    }

    @objc func actionStop(_ sender: NSMenuItem) {
        Task { @MainActor in
            await vmModel.tryDockerContainerStop(container.id)
        }
    }

    @objc func actionRestart(_ sender: NSMenuItem) {
        Task { @MainActor in
            await vmModel.tryDockerContainerRestart(container.id)
        }
    }

    @objc func actionDelete(_ sender: NSMenuItem) {
        Task { @MainActor in
            await vmModel.tryDockerContainerRemove(container.id)
        }
    }

    @objc func actionShowLogs(_ sender: NSMenuItem) {
        Task { @MainActor in
            container.showLogs(vmModel: vmModel)
        }
    }

    @objc func actionOpenTerminal(_ sender: NSMenuItem) {
        Task { @MainActor in
            container.openInTerminal()
        }
    }

    @objc func actionCopyRunCommand(_ sender: NSMenuItem) {
        Task { @MainActor in
            await container.copyRunCommand()
        }
    }
}

private class DockerPortMenuItemController: NSObject {
    private let port: DKPort

    init(port: DKPort) {
        self.port = port
        super.init()
    }

    @objc func actionOpen(_ sender: NSMenuItem) {
        Task { @MainActor in
            port.openUrl()
        }
    }
}

private class DockerMountMenuItemController: NSObject {
    private let mount: DKMountPoint

    init(mount: DKMountPoint) {
        self.mount = mount
        super.init()
    }

    @objc func actionOpen(_ sender: NSMenuItem) {
        Task { @MainActor in
            mount.openSourceDirectory()
        }
    }
}