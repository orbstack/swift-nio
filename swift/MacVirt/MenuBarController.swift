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
    private var lastSyntheticVmState = VmState.stopped
    private var isAnimating = false
    private var lastTargetIsActive = false
    var quitInitiated = false
    var quitForce = false

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

            // start in stopped state
            button.alphaValue = opacityAppearsDisabled
        }
        statusItem.menu = menu
        menu.delegate = self

        // observe relevant states
        Task { @MainActor in
            for await state in vmModel.$state.values {
                // we don't need to trigger any Docker refreshes here.
                // 3 cases:
                // - already running when GUI started
                //   - SwiftUI ContentView .onAppear will trigger list refresh
                // - was started by GUI
                //   - refresh also triggered by ContentView
                // - CLI started in the background, GUI already running
                //   - will dispatch docker UI change event

                let syntheticState = deriveSyntheticVmState(vmState: state,
                        machines: vmModel.containers,
                        dockerContainers: vmModel.dockerContainers)
                updateSyntheticVmState(syntheticState)
            }
        }
        // need to observe these too, to exit synthetic starting state at the right time
        Task { @MainActor in
            for await machines in vmModel.$containers.values {
                let syntheticState = deriveSyntheticVmState(vmState: vmModel.state,
                        machines: machines,
                        dockerContainers: vmModel.dockerContainers)
                updateSyntheticVmState(syntheticState)
            }
        }
        Task { @MainActor in
            for await dockerContainers in vmModel.$dockerContainers.values {
                let syntheticState = deriveSyntheticVmState(vmState: vmModel.state,
                        machines: vmModel.containers,
                        dockerContainers: dockerContainers)
                updateSyntheticVmState(syntheticState)
            }
        }
    }

    private func deriveSyntheticVmState(vmState: VmState,
                                        machines: [ContainerRecord]?,
                                        dockerContainers: [DKContainer]?) -> VmState {
        // check for machine and docker containers too
        // if we're waiting for any to load, then still consider it starting for animation purposes
        if vmState != .running {
            // only running needs synthetic treatment
            return vmState
        }

        // check for machines
        guard machines != nil else {
            return .starting
        }

        // check for docker if it's enabled
        if vmModel.isDockerRunning() {
            guard dockerContainers != nil else {
                return .starting
            }
        }

        return .running
    }

    private func updateSyntheticVmState(_ state: VmState) {
        print("set synthetic state \(state)")
        if state == lastSyntheticVmState {
            return
        }

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

        lastSyntheticVmState = state
    }

    private func startAnimation() {
        if isAnimating {
            return
        }

        isAnimating = true
        animationStep()
    }

    private func stopAnimation() {
        // set final alpha if we never animated (e.g. direct jump to stopped)
        // if we were animating, wait for it to finish for smooth transition
        if !isAnimating {
            statusItem.button?.alphaValue = lastTargetIsActive ? 1 : opacityAppearsDisabled
        }

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

        menu.addActionItem("Open OrbStack", shortcut: "n", icon: systemImage("sidebar.leading")) { [self] in
            openApp()
        }

        menu.addSeparator()

        // Docker containers
        if let dockerContainers = vmModel.dockerContainers {
            menu.addSectionHeader("Containers")

            // group by Compose
            let listItems = DockerContainerLists.makeListItems(filteredContainers: dockerContainers,
                    // menu bar never shows stopped
                    allowShowStopped: false)
            menu.addTruncatedItems(listItems) { item in
                if let container = item.container {
                    return makeContainerItem(container: container)
                } else if let composeGroup = item.composeGroup {
                    return makeComposeGroupItem(group: composeGroup, children: item.children!)
                } else {
                    // other types are invalid
                    return nil
                }
            }

            // placeholder if no containers
            if listItems.isEmpty {
                menu.addInfoLine("None running")
            }

            menu.addSeparator()
        }

        // Machines (exclude docker)
        if let machines = vmModel.containers,
           machines.contains(where: { !$0.builtin }) {
            menu.addSectionHeader("Machines")

            // only show running in menu bar
            let runningMachines = machines.filter { $0.running && !$0.builtin }
            menu.addTruncatedItems(runningMachines) { machine in
                makeMachineItem(record: machine)
            }

            // placeholder if no machines
            if runningMachines.isEmpty {
                menu.addInfoLine("None running")
            }

            menu.addSeparator()
        }

        menu.addActionItem("Check for Updates…") { [self] in
            updaterController.checkForUpdates(nil)
            NSApp.activate(ignoringOtherApps: true)
        }

        menu.addActionItem("Settings…") {
            if #available(macOS 13, *) {
                NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
            } else {
                NSApp.sendAction(Selector(("showPreferencesWindow:")), to: nil, from: nil)
            }

            // focus app
            NSApp.activate(ignoringOtherApps: true)
        }

        menu.addActionItem("Quit", shortcut: "q") { [self] in
            // opt = force quit
            if CGKeyCode.optionKeyPressed {
                quitForce = true
            }

            // quick-quit logic for user-initiated menu bar quit
            quitInitiated = true
            NSApp.terminate(self)
        }
    }

    private func makeContainerItem(container: DKContainer) -> NSMenuItem {
        let actionInProgress = actionTracker.ongoingFor(container.cid) != nil

        // TODO: highlight container item and open popover
        let containerItem = newActionItem(container.userName) { [self] in
            openApp(tab: "docker")
        }
        let submenu = containerItem.newSubmenu()

        submenu.addActionItem("ID: \(container.id.prefix(12))") {
            NSPasteboard.copy(container.id)
        }

        // in case of pinned hashes
        let truncatedImage = container.image.prefix(32) +
                (container.image.count > 32 ? "…" : "")
        submenu.addActionItem("Image: \(truncatedImage)") {
            NSPasteboard.copy(container.image)
        }

        if let ipAddress = container.ipAddresses.first {
            submenu.addActionItem("IP: \(ipAddress)") {
                NSPasteboard.copy(ipAddress)
            }
        }

        submenu.addSeparator()

        if container.running {
            submenu.addActionItem("Stop", icon: systemImage("stop.fill"),
                    disabled: actionInProgress) { [self] in
                await actionTracker.with(cid: container.cid, action: .stop) {
                    await vmModel.tryDockerContainerStop(container.id)
                }
            }
        } else {
            submenu.addActionItem("Start", icon: systemImage("play.fill"),
                    disabled: actionInProgress) { [self] in
                await actionTracker.with(cid: container.cid, action: .start) {
                    await vmModel.tryDockerContainerStart(container.id)
                }
            }
        }

        submenu.addActionItem("Restart", icon: systemImage("arrow.clockwise"),
                disabled: actionInProgress || !container.running) { [self] in
            await actionTracker.with(cid: container.cid, action: .restart) {
                await vmModel.tryDockerContainerRestart(container.id)
            }
        }

        submenu.addActionItem("Delete", icon: systemImage("trash.fill"),
                disabled: actionInProgress) { [self] in
            await actionTracker.with(cid: container.cid, action: .remove) {
                await vmModel.tryDockerContainerRemove(container.id)
            }
        }

        submenu.addSeparator()

        submenu.addActionItem("Show Logs") { [self] in
            container.showLogs(vmModel: vmModel)
        }

        submenu.addActionItem("Open Terminal", disabled: !container.running) {
            container.openInTerminal()
        }

        submenu.addSeparator()

        if !container.ports.isEmpty {
            submenu.addItem(makePortsItem(ports: container.ports))
        }
        if !container.mounts.isEmpty {
            submenu.addItem(makeMountsItem(mounts: container.mounts))
        }
        if container.ports.isEmpty && container.mounts.isEmpty {
            submenu.addInfoLine("No Ports or Mounts")
        }

        return containerItem
    }

    private func makeComposeGroupItem(group: ComposeGroup, children: [DockerListItem]) -> NSMenuItem {
        let groupItem = newActionItem(group.project, icon: systemImage("square.stack.3d.up.fill")) { [self] in
            openApp(tab: "docker")
        }
        let submenu = groupItem.newSubmenu()

        let actionInProgress = actionTracker.ongoingFor(group.cid) != nil

        // actions
        if group.anyRunning {
            submenu.addActionItem("Stop", icon: systemImage("stop.fill"),
                    disabled: actionInProgress) { [self] in
                await actionTracker.with(cid: group.cid, action: .stop) {
                    await vmModel.tryDockerComposeStop(group.cid)
                }
            }
        } else {
            submenu.addActionItem("Start", icon: systemImage("play.fill"),
                    disabled: actionInProgress) { [self] in
                await actionTracker.with(cid: group.cid, action: .start) {
                    await vmModel.tryDockerComposeStart(group.cid)
                }
            }
        }

        submenu.addActionItem("Restart", icon: systemImage("arrow.clockwise"),
                disabled: actionInProgress) { [self] in
            await actionTracker.with(cid: group.cid, action: .restart) {
                await vmModel.tryDockerComposeRestart(group.cid)
            }
        }

        submenu.addActionItem("Delete", icon: systemImage("trash.fill"),
                disabled: actionInProgress) { [self] in
            await actionTracker.with(cid: group.cid, action: .remove) {
                await vmModel.tryDockerComposeRemove(group.cid)
            }
        }

        submenu.addSeparator()
        submenu.addSectionHeader("Services")

        for childItem in children {
            guard let container = childItem.container else {
                continue
            }

            let item = makeContainerItem(container: container)
            submenu.addItem(item)
        }

        return groupItem
    }

    private func makePortsItem(ports: [DKPort]) -> NSMenuItem {
        let portsItem = NSMenuItem()
        portsItem.title = "Ports"
        let submenu = portsItem.newSubmenu()

        for port in ports {
            submenu.addActionItem(port.formatted) {
                port.openUrl()
            }
        }

        return portsItem
    }

    private func makeMountsItem(mounts: [DKMountPoint]) -> NSMenuItem {
        let mountsItem = NSMenuItem()
        mountsItem.title = "Mounts"
        let submenu = mountsItem.newSubmenu()

        for mount in mounts {
            submenu.addActionItem(mount.formatted) {
                mount.openSourceDirectory()
            }
        }

        return mountsItem
    }

    private func makeMachineItem(record: ContainerRecord) -> NSMenuItem {
        let actionInProgress = actionTracker.ongoingFor(machine: record) != nil

        let machineItem = newActionItem(record.name) {
            await record.openInTerminal()
        }
        let submenu = machineItem.newSubmenu()

        if record.running {
            submenu.addActionItem("Stop", icon: systemImage("stop.fill"),
                    disabled: actionInProgress) { [self] in
                await actionTracker.with(machine: record, action: .stop) {
                    await vmModel.tryStopContainer(record)
                }
            }
        } else {
            submenu.addActionItem("Start", icon: systemImage("play.fill"),
                    disabled: actionInProgress) { [self] in
                await actionTracker.with(machine: record, action: .start) {
                    await vmModel.tryStartContainer(record)
                }
            }
        }

        submenu.addActionItem("Restart", icon: systemImage("arrow.clockwise"),
                disabled: actionInProgress || !record.running) { [self] in
            await actionTracker.with(machine: record, action: .restart) {
                await vmModel.tryRestartContainer(record)
            }
        }

        // machine delete is too destructive for menu

        submenu.addSeparator()

        submenu.addActionItem("Open Terminal") {
            await record.openInTerminal()
        }

        submenu.addActionItem("Open Files") {
            record.openNfsDirectory()
        }

        return machineItem
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
}

private extension NSMenu {
    func addTruncatedItems<T>(_ items: [T], makeItem: (T) -> NSMenuItem?) {
        // limit 5
        for container in items.prefix(maxQuickAccessItems) {
            let item = makeItem(container)
            if let item {
                self.addItem(item)
            }
        }

        // show extras in submenu
        if items.count > maxQuickAccessItems {
            let submenu = NSMenu()
            let extraItem = NSMenuItem(title: "\(items.count - maxQuickAccessItems) more",
                    action: nil,
                    keyEquivalent: "")
            extraItem.image = systemImage("ellipsis")
            extraItem.submenu = submenu
            self.addItem(extraItem)

            for container in items.dropFirst(maxQuickAccessItems) {
                let item = makeItem(container)
                if let item {
                    submenu.addItem(item)
                }
            }
        }
    }

    func addInfoLine(_ text: String) {
        let item = NSMenuItem(title: text, action: nil, keyEquivalent: "")
        item.isEnabled = false
        self.addItem(item)
    }

    func addSectionHeader(_ title: String) {
        let item = NSMenuItem()
        // use attributedTitle for emphasis
        item.attributedTitle = NSAttributedString(string: title, attributes: [
            NSAttributedString.Key.font: NSFont.systemFont(ofSize: 12, weight: .bold),
            NSAttributedString.Key.foregroundColor: NSColor.labelColor
        ])
        item.isEnabled = false
        self.addItem(item)
    }

    func addSeparator() {
        self.addItem(NSMenuItem.separator())
    }

    func addActionItem(_ title: String,
                       shortcut: String = "",
                       icon: NSImage? = nil,
                       disabled: Bool = false,
                       action: @escaping () -> Void) {
        self.addItem(newActionItem(title,
                shortcut: shortcut,
                icon: icon,
                disabled: disabled,
                action: action))
    }

    func addActionItem(_ title: String,
                       shortcut: String = "",
                       icon: NSImage? = nil,
                       disabled: Bool = false,
                       asyncAction: @escaping () async -> Void) {
        self.addItem(newActionItem(title,
                shortcut: shortcut,
                icon: icon,
                disabled: disabled,
                asyncAction: asyncAction))
    }
}

private extension NSMenuItem {
    func newSubmenu() -> NSMenu {
        let submenu = NSMenu()
        // let us control enable/disable by disabled flag
        submenu.autoenablesItems = false
        self.submenu = submenu
        return submenu
    }
}

private func newActionItem(_ title: String,
                   shortcut: String = "",
                   icon: NSImage? = nil,
                   disabled: Bool = false,
                   action: @escaping () -> Void) -> NSMenuItem {
    let controller = ActionItemController(action: action)
    let item = NSMenuItem(title: title, action: #selector(controller.action),
            keyEquivalent: shortcut)
    item.target = controller
    item.image = icon
    item.isEnabled = !disabled
    // retain
    item.representedObject = controller
    return item
}

private func newActionItem(_ title: String,
                           shortcut: String = "",
                           icon: NSImage? = nil,
                           disabled: Bool = false,
                           asyncAction: @escaping () async -> Void) -> NSMenuItem {
    return newActionItem(title,
            shortcut: shortcut,
            icon: icon,
            disabled: disabled) {
        Task { @MainActor in
            await asyncAction()
        }
    }
}

private class ActionItemController: NSObject {
    private let action: () -> Void

    init(action: @escaping () -> Void) {
        self.action = action
        super.init()
    }

    @objc func action(_ sender: NSMenuItem) {
        action()
    }
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