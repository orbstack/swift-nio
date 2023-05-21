//
// Created by Danny Lin on 5/20/23.
//

import Foundation
import Sparkle
import AppKit
import Defaults
import Combine
import SwiftUI

private let maxQuickAccessItems = 5
private let pulseAnimationPeriod: TimeInterval = 0.75
// close to NSStatusBarButton.opacityWhenDisabled
private let opacityAppearsDisabled = 0.3

// must be @MainActor to access view model
@MainActor
class MenuBarController: NSObject, NSMenuDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    private let menu = NSMenu()

    private let updaterController: SPUStandardUpdaterController
    private let actionTracker: ActionTracker
    private let windowTracker: WindowTracker
    private let vmModel: VmViewModel

    private var cancellables = Set<AnyCancellable>()
    private var isAnimating = false
    private var lastTargetIsActive = false

    init(updaterController: SPUStandardUpdaterController,
         actionTracker: ActionTracker, windowTracker: WindowTracker, vmModel: VmViewModel) {
        self.updaterController = updaterController
        self.actionTracker = actionTracker
        self.windowTracker = windowTracker
        self.vmModel = vmModel
        super.init()

        // follow user setting
        statusItem.isVisible = Defaults[.globalShowMenubarExtra]
        cancellables.insert(UserDefaults.standard.publisher(for: \.globalShowMenubarExtra)
                .sink(receiveValue: { [weak self] newValue in
                    guard let self = self else { return }
                    self.statusItem.isVisible = newValue
                }))

        if let button = statusItem.button {
            // bold = larger, matches other menu bar icons
            // circle.hexagongrid.circle?
            button.image = systemImage("circle.circle.fill", bold: true)
            //button.appearsDisabled = true
            animationStep()
        }
        statusItem.menu = menu
        menu.delegate = self

        // observe state
        Task { @MainActor in
            for await state in vmModel.$state.values {
                // TODO if we just hit running, and Docker is enabled, trigger a docker refresh

                switch state {
                case .stopped:
                    lastTargetIsActive = false
                    stopAnimation()
                case .spawning, .starting:
                    lastTargetIsActive = true
                    startAnimation()
                case .running:
                    lastTargetIsActive = true
                    stopAnimation()
                case .stopping:
                    lastTargetIsActive = false
                    startAnimation()
                }
            }
        }
    }

    private func startAnimation() {
        if isAnimating {
            return
        }

        isAnimating = true
        animationStep()
    }

    private func stopAnimation() {
        isAnimating = false
    }

    func hide() {
        NSStatusBar.system.removeStatusItem(statusItem)
    }

    private func animationStep() {
        // pulsing animation
        NSAnimationContext.runAnimationGroup { context in
            context.duration = pulseAnimationPeriod
            self.statusItem.button?.animator().alphaValue = lastTargetIsActive ? opacityAppearsDisabled : 1
        } completionHandler: { [self] in
            NSAnimationContext.runAnimationGroup { context in
                context.duration = pulseAnimationPeriod
                self.statusItem.button?.animator().alphaValue = lastTargetIsActive ? 1 : opacityAppearsDisabled
            } completionHandler: { [self] in
                // do we go for another cycle?
                // if not, leave it at the last target opacity
                if isAnimating {
                    self.animationStep()
                }
            }
        }
    }

    func menuWillOpen(_ menu: NSMenu) {
        updateMenu()

        // also trigger a background machine state refresh
        // TODO: remove when we have dynamic machine state updates
        Task {
            await vmModel.tryRefreshList()
        }
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
            for container in runningContainers.prefix(maxQuickAccessItems) {
                let item = makeContainerItem(container: container)
                menu.addItem(item)
            }

            // show extras in submenu
            if runningContainers.count > maxQuickAccessItems {
                let submenu = NSMenu()
                let extraItem = NSMenuItem(title: "\(runningContainers.count - maxQuickAccessItems) more",
                        action: nil,
                        keyEquivalent: "")
                extraItem.image = systemImage("ellipsis")
                extraItem.submenu = submenu
                menu.addItem(extraItem)

                for container in runningContainers.dropFirst(maxQuickAccessItems) {
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

        // Machines (exclude docker)
        if let machines = vmModel.containers,
           machines.contains(where: { !$0.builtin }) {
            menu.addItem(makeSectionTitleItem(title: "Machines"))

            // only show running in menu bar
            let runningMachines = machines.filter { $0.running && !$0.builtin }

            // limit 5
            for machine in runningMachines.prefix(maxQuickAccessItems) {
                let item = makeMachineItem(record: machine)
                menu.addItem(item)
            }

            // show extras in submenu
            if runningMachines.count > maxQuickAccessItems {
                let submenu = NSMenu()
                let extraItem = NSMenuItem(title: "\(runningMachines.count - maxQuickAccessItems) more",
                        action: nil,
                        keyEquivalent: "")
                extraItem.image = systemImage("ellipsis")
                extraItem.submenu = submenu
                menu.addItem(extraItem)

                for machine in runningMachines.dropFirst(maxQuickAccessItems) {
                    let item = makeMachineItem(record: machine)
                    submenu.addItem(item)
                }
            }

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
                action: #selector(actionCheckForUpdates),
                keyEquivalent: "")
        updateItem.target = self
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
        let controller = DockerContainerMenuItemController(container: container,
                actionTracker: actionTracker, vmModel: vmModel)
        let actionInProgress = actionTracker.ongoingFor(container.cid) != nil

        let containerItem = NSMenuItem()
        containerItem.title = container.userName
        // TODO: actionShowContainerInfo
        containerItem.target = self
        containerItem.action = #selector(actionOpenAppAtContainers)
        // keep ref
        containerItem.representedObject = controller

        let submenu = NSMenu()
        // enable/disable by actionInProgress
        submenu.autoenablesItems = false
        containerItem.submenu = submenu

        let copyIDItem = NSMenuItem(title: "ID: \(container.id.prefix(12))",
                action: #selector(actionCopyString),
                keyEquivalent: "")
        copyIDItem.target = self
        copyIDItem.representedObject = container.id
        submenu.addItem(copyIDItem)

        // in case of pinned hashes
        let truncatedImage = container.image.prefix(32) +
                (container.image.count > 32 ? "…" : "")
        let copyNameItem = NSMenuItem(title: "Image: \(truncatedImage)",
                action: #selector(actionCopyString),
                keyEquivalent: "")
        copyNameItem.target = self
        copyNameItem.representedObject = container.image
        submenu.addItem(copyNameItem)

        if let ipAddress = container.ipAddresses.first {
            let copyIpItem = NSMenuItem(title: "IP: \(ipAddress)",
                    action: #selector(actionCopyString),
                    keyEquivalent: "")
            copyIpItem.target = self
            copyIpItem.representedObject = ipAddress
            submenu.addItem(copyIpItem)
        }

        submenu.addItem(NSMenuItem.separator())

        if container.running {
            let stopItem = NSMenuItem(title: "Stop",
                    action: #selector(controller.actionStop),
                    keyEquivalent: "")
            stopItem.target = controller
            stopItem.image = systemImage("stop.fill")
            stopItem.isEnabled = !actionInProgress
            submenu.addItem(stopItem)
        } else {
            let startItem = NSMenuItem(title: "Start",
                    action: #selector(controller.actionStart),
                    keyEquivalent: "")
            startItem.target = controller
            startItem.image = systemImage("play.fill")
            startItem.isEnabled = !actionInProgress
            submenu.addItem(startItem)
        }

        let restartItem = NSMenuItem(title: "Restart",
                action: #selector(controller.actionRestart),
                keyEquivalent: "")
        restartItem.target = controller
        restartItem.image = systemImage("arrow.clockwise")
        restartItem.isEnabled = container.running && !actionInProgress
        submenu.addItem(restartItem)

        let deleteItem = NSMenuItem(title: "Delete",
                action: #selector(controller.actionDelete),
                keyEquivalent: "")
        deleteItem.target = controller
        deleteItem.image = systemImage("trash.fill")
        deleteItem.isEnabled = !actionInProgress
        submenu.addItem(deleteItem)

        submenu.addItem(NSMenuItem.separator())

        let logsItem = NSMenuItem(title: "Show Logs",
                action: #selector(controller.actionShowLogs),
                keyEquivalent: "")
        logsItem.target = controller
        submenu.addItem(logsItem)

        let terminalItem = NSMenuItem(title: "Open Terminal",
                action: #selector(controller.actionOpenTerminal),
                keyEquivalent: "")
        terminalItem.target = controller
        terminalItem.isEnabled = container.running
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

    private func makeMachineItem(record: ContainerRecord) -> NSMenuItem {
        let controller = MachineMenuItemController(record: record,
                actionTracker: actionTracker, vmModel: vmModel)
        let actionInProgress = actionTracker.ongoingFor(machine: record) != nil

        let machineItem = NSMenuItem()
        machineItem.title = record.name
        machineItem.target = controller
        machineItem.action = #selector(controller.actionOpenTerminal)
        // keep ref
        machineItem.representedObject = controller

        let submenu = NSMenu()
        // enable/disable by actionInProgress
        submenu.autoenablesItems = false
        machineItem.submenu = submenu

        /*
        if let distro = Distro(rawValue: record.image.distro) {
            let optVersion = record.image.version == "current" ? "" : " \(record.image.version)"
            let distroItem = NSMenuItem(title: "Distro: \(distro.friendlyName)\(optVersion)",
                    action: nil,
                    keyEquivalent: "")
            distroItem.isEnabled = false
            submenu.addItem(distroItem)
        }

        submenu.addItem(NSMenuItem.separator())
         */

        if record.running {
            let stopItem = NSMenuItem(title: "Stop",
                    action: #selector(controller.actionStop),
                    keyEquivalent: "")
            stopItem.target = controller
            stopItem.image = systemImage("stop.fill")
            stopItem.isEnabled = !actionInProgress
            submenu.addItem(stopItem)
        } else {
            let startItem = NSMenuItem(title: "Start",
                    action: #selector(controller.actionStart),
                    keyEquivalent: "")
            startItem.target = controller
            startItem.image = systemImage("play.fill")
            startItem.isEnabled = !actionInProgress
            submenu.addItem(startItem)
        }

        let restartItem = NSMenuItem(title: "Restart",
                action: #selector(controller.actionRestart),
                keyEquivalent: "")
        restartItem.target = controller
        restartItem.image = systemImage("arrow.clockwise")
        restartItem.isEnabled = record.running && !actionInProgress
        submenu.addItem(restartItem)

        // machine delete is too destructive for menu

        submenu.addItem(NSMenuItem.separator())

        let terminalItem = NSMenuItem(title: "Open Terminal",
                action: #selector(controller.actionOpenTerminal),
                keyEquivalent: "")
        terminalItem.target = controller
        submenu.addItem(terminalItem)

        let filesItem = NSMenuItem(title: "Open Files",
                action: #selector(controller.actionOpenFiles),
                keyEquivalent: "")
        filesItem.target = controller
        submenu.addItem(filesItem)

        return machineItem
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

    private func openApp(tab: String? = nil) {
        if let tab {
            // set UserDefaults
            UserDefaults.standard.set(tab, forKey: "root.selectedTab")
        }

        // reappear in dock
        windowTracker.setPolicy(.regular)

        // open main window if needed, as if user clicked on dock
        // but always open main so users can get back to main, not e.g. logs
        // must have both because onDisappear (count) is called lazily
        if !NSApp.windows.contains(where: { $0.isUserFacing }) || vmModel.openMainWindowCount == 0 {
            NSLog("open main")
            NSWorkspace.shared.open(URL(string: "orbstack://main")!)
        }
    }

    @objc private func actionOpenApp() {
        openApp()
    }

    @objc private func actionOpenAppAtMachines() {
        openApp(tab: "machines")
    }

    @objc private func actionOpenAppAtContainers() {
        openApp(tab: "docker")
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

    @objc private func actionCopyString(_ sender: NSMenuItem) {
        if let string = sender.representedObject as? String {
            NSPasteboard.copy(string)
        }
    }

    @objc private func actionCheckForUpdates(_ sender: NSMenuItem) {
        updaterController.checkForUpdates(updaterController)
        NSApp.activate(ignoringOtherApps: true)
    }
}

private class DockerContainerMenuItemController: NSObject {
    private let container: DKContainer
    private let actionTracker: ActionTracker
    private let vmModel: VmViewModel

    init(container: DKContainer, actionTracker: ActionTracker, vmModel: VmViewModel) {
        self.container = container
        self.actionTracker = actionTracker
        self.vmModel = vmModel
        super.init()
    }

    @objc func actionStart(_ sender: NSMenuItem) {
        Task { @MainActor in
            await actionTracker.with(cid: container.cid, action: .start) {
                await vmModel.tryDockerContainerStart(container.id)
            }
        }
    }

    @objc func actionStop(_ sender: NSMenuItem) {
        Task { @MainActor in
            await actionTracker.with(cid: container.cid, action: .stop) {
                await vmModel.tryDockerContainerStop(container.id)
            }
        }
    }

    @objc func actionRestart(_ sender: NSMenuItem) {
        Task { @MainActor in
            await actionTracker.with(cid: container.cid, action: .restart) {
                await vmModel.tryDockerContainerRestart(container.id)
            }
        }
    }

    @objc func actionDelete(_ sender: NSMenuItem) {
        Task { @MainActor in
            await actionTracker.with(cid: container.cid, action: .remove) {
                await vmModel.tryDockerContainerRemove(container.id)
            }
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

    @objc func actionShowContainerInfo(_ sender: NSMenuItem) {
        // TODO unstable: assertion failure in NSToolbar, and bad behavior with compose groups
        NSWorkspace.shared.open(URL(string: "orbstack://docker/containers/\(container.id)")!)
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

private class MachineMenuItemController: NSObject {
    private let record: ContainerRecord
    private let actionTracker: ActionTracker
    private let vmModel: VmViewModel

    init(record: ContainerRecord, actionTracker: ActionTracker, vmModel: VmViewModel) {
        self.record = record
        self.actionTracker = actionTracker
        self.vmModel = vmModel
        super.init()
    }

    @objc func actionStart(_ sender: NSMenuItem) {
        Task { @MainActor in
            await actionTracker.with(machine: record, action: .start) {
                await vmModel.tryStartContainer(record)
            }
        }
    }

    @objc func actionStop(_ sender: NSMenuItem) {
        Task { @MainActor in
            await actionTracker.with(machine: record, action: .stop) {
                await vmModel.tryStopContainer(record)
            }
        }
    }

    @objc func actionRestart(_ sender: NSMenuItem) {
        Task { @MainActor in
            await actionTracker.with(machine: record, action: .restart) {
                await vmModel.tryRestartContainer(record)
            }
        }
    }

    @objc func actionDelete(_ sender: NSMenuItem) {
        Task { @MainActor in
            await actionTracker.with(machine: record, action: .delete) {
                await vmModel.tryDeleteContainer(record)
            }
        }
    }

    @objc func actionOpenTerminal(_ sender: NSMenuItem) {
        Task { @MainActor in
            await record.openInTerminal()
        }
    }

    @objc func actionOpenFiles(_ sender: NSMenuItem) {
        Task { @MainActor in
            record.openNfsDirectory()
        }
    }

    @objc func actionShowMachineInfo(_ sender: NSMenuItem) {
        // TODO unstable: assertion failure in NSToolbar
        NSWorkspace.shared.open(URL(string: "orbstack://machines/\(record.id)")!)
    }
}