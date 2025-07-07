//
// Created by Danny Lin on 5/20/23.
//

import AppKit
import Combine
import Defaults
import Foundation
import Sparkle
import SwiftUI

private let maxQuickAccessItems = 5
private let lineLimit = 32

private let pulseAnimationPeriod: TimeInterval = 0.5
// close to NSStatusBarButton.opacityWhenDisabled
private let opacityAppearsDisabled = 0.3

// must be @MainActor to access view model
@MainActor
class MenuBarController: NSObject, NSMenuDelegate {
    private let updaterController: SPUStandardUpdaterController
    private let actionTracker: ActionTracker
    private let windowTracker: WindowTracker
    private let vmModel: VmViewModel

    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    private let menu = NSMenu()

    private var bgTipPopover: NSPopover?

    private var visibleObservation: NSKeyValueObservation?

    private var lastSyntheticVmState = VmState.stopped
    private var isAnimating = false
    private var lastTargetIsActive = false

    private var cancellables: Set<AnyCancellable> = []

    init(
        updaterController: SPUStandardUpdaterController,
        actionTracker: ActionTracker, windowTracker: WindowTracker, vmModel: VmViewModel
    ) {
        self.updaterController = updaterController
        self.actionTracker = actionTracker
        self.windowTracker = windowTracker
        self.vmModel = vmModel
        super.init()

        // follow user setting
        statusItem.behavior = .removalAllowed
        statusItem.isVisible = Defaults[.globalShowMenubarExtra]
        Task { @MainActor in
            for await newValue in Defaults.updates(.globalShowMenubarExtra, initial: false) {
                statusItem.isVisible = newValue
            }
        }

        // change setting if user removes from menu bar
        // by observing .isVisible with KVO
        visibleObservation = statusItem.observe(\.isVisible, options: [.new]) { _, change in
            if let newValue = change.newValue {
                let settingValue = Defaults[.globalShowMenubarExtra]
                if newValue != settingValue {
                    NSLog("update menu bar setting from \(settingValue) to \(newValue)")
                    Defaults[.globalShowMenubarExtra] = newValue
                }
            }
        }

        if let button = statusItem.button {
            // bold = larger, matches other menu bar icons
            // circle.hexagongrid.circle?
            // systemImage gets cut off with mixed DPI external display
            button.image = NSImage(named: "MenuBarIcon")

            // start in stopped state
            button.alphaValue = opacityAppearsDisabled
        }
        statusItem.menu = menu
        menu.delegate = self

        // observe relevant states
        vmModel.$state
            .combineLatest(vmModel.$machines, vmModel.$dockerContainers, vmModel.$isVmRestarting) {
                ($0, $1, $2, $3)
            }
            .sink { [self] state, machines, dockerContainers, isVmRestarting in
                // we don't need to trigger any Docker refreshes here.
                // 3 cases:
                // - already running when GUI started
                //   - SwiftUI ContentView .onAppear will trigger list refresh
                // - was started by GUI
                //   - refresh also triggered by ContentView
                // - CLI started in the background, GUI already running
                //   - will dispatch docker UI change event

                let syntheticState = deriveSyntheticVmState(
                    vmState: state,
                    machines: machines,
                    dockerContainers: dockerContainers,
                    isVmRestarting: isVmRestarting)
                updateSyntheticVmState(syntheticState)
            }
            .store(in: &cancellables)
    }

    private func deriveSyntheticVmState(
        vmState: VmState,
        machines: [String: ContainerInfo]?,
        dockerContainers: MappedContainers?,
        isVmRestarting: Bool
    ) -> VmState {
        if isVmRestarting {
            return .starting
        }

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
        if state == lastSyntheticVmState {
            return
        }
        NSLog("synthetic state -> \(state)")

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

    private func animationStep(odd: Bool = true) {
        // pulsing animation
        let newTarget = odd ? opacityAppearsDisabled : 1
        NSAnimationContext.runAnimationGroup { context in
            context.duration = pulseAnimationPeriod
            self.statusItem.button?.animator().alphaValue = newTarget
        } completionHandler: { [self] in
            // if still animating, or stopped but current value is wrong, then go for another cycle
            // otherwise we're done - set final value
            if isAnimating || ((newTarget == 1) != lastTargetIsActive) {
                self.animationStep(odd: !odd)
            } else {
                self.statusItem.button?.alphaValue = lastTargetIsActive ? 1 : opacityAppearsDisabled
            }
        }
    }

    func menuWillOpen(_: NSMenu) {
        updateMenu()
        closeBgTip()
    }

    private func updateMenu() {
        menu.removeAllItems()

        menu.addActionItem("Open OrbStack", shortcut: "n", icon: systemImage("sidebar.leading")) {
            [self] in
            openApp()
        }

        menu.addSeparator()

        // snapshot for atomicity
        let state = lastSyntheticVmState
        if state != .running {
            menu.addSectionHeader("Status: \(state.userState)")
        }

        if state == .stopped {
            menu.addActionItem("Start", shortcut: "s", icon: systemImage("play.fill")) { [self] in
                await vmModel.tryStartDaemon()
            }
        }

        menu.addSeparator()

        // Docker containers
        if let dockerContainers = vmModel.dockerContainers {
            menu.addSectionHeader("Containers")
            let (runningItems, stoppedItems) = DockerContainerLists.makeListItems(
                filteredContainers: dockerContainers.byId.values, dockerFilterShowStopped: false)

            // placeholder if no containers
            if runningItems.isEmpty
                || runningItems.allSatisfy({
                    if case .k8sGroup = $0 { return true } else { return false }
                })
            {
                menu.addInfoLine("None running")
            }

            // group by Compose
            menu.addTruncatedItems(
                runningItems, overflowHeader: "Stopped", overflowItems: stoppedItems
            ) { item in
                switch item {
                case let .container(container):
                    return makeContainerItem(
                        container: container, collapseIfStopped: true /* overflow */)
                case let .compose(group, children):
                    return makeComposeGroupItem(group: group, children: children)
                case .k8sGroup:
                    // ignore
                    return nil
                default:
                    return nil
                }
            }

            menu.addSeparator()
        }

        // Machines (exclude docker)
        if let machines = vmModel.machines,
            machines.values.contains(where: { !$0.record.builtin })
        {
            menu.addSectionHeader("Machines")

            let runningMachines = machines.values.filter { $0.record.running && !$0.record.builtin }
            let stoppedMachines = machines.values.filter {
                !$0.record.running && !$0.record.builtin
            }

            // placeholder if no machines
            if runningMachines.isEmpty {
                menu.addInfoLine("None running")
            }

            menu.addTruncatedItems(
                runningMachines, overflowHeader: "Stopped", overflowItems: stoppedMachines
            ) { machine in
                makeMachineItem(record: machine.record)
            }

            menu.addSeparator()
        }

        let helpItem = NSMenuItem(title: "Help", action: nil, keyEquivalent: "")
        helpItem.image = systemImage("questionmark.circle")
        menu.addItem(helpItem)
        let helpMenu = helpItem.newSubmenu()
        helpMenu.addActionItem("Documentation", icon: systemImage("book.closed")) {
            NSWorkspace.shared.open(URL(string: "https://docs.orbstack.dev")!)
        }
        helpMenu.addActionItem("Community", icon: systemImage("message")) {
            NSWorkspace.shared.open(URL(string: "https://orbstack.dev/chat")!)
        }
        helpMenu.addActionItem("Email", icon: systemImage("envelope")) {
            NSWorkspace.shared.open(URL(string: "mailto:support@orbstack.dev")!)
        }

        helpMenu.addSeparator()

        helpMenu.addActionItem("Report Bug", icon: systemImage("exclamationmark.triangle")) {
            NSWorkspace.openSubwindow(WindowID.bugReport)
        }
        helpMenu.addActionItem("Request Feature", icon: systemImage("lightbulb")) {
            NSWorkspace.shared.open(URL(string: "https://orbstack.dev/issues/feature")!)
        }
        helpMenu.addActionItem("Send Feedback", icon: systemImage("paperplane")) {
            NSWorkspace.openSubwindow(WindowID.feedback)
        }

        helpMenu.addSeparator()

        helpMenu.addActionItem("Restart", shortcut: "r", icon: systemImage("arrow.clockwise")) {
            [self] in
            await vmModel.tryRestart()
        }

        helpMenu.addActionItem(
            "Sign Out", icon: systemImage("person.crop.circle"),
            disabled: !vmModel.drmState.isSignedIn
        ) { [self] in
            await vmModel.trySignOut()
        }

        helpMenu.addActionItem("Upload Diagnostics", icon: systemImage("ladybug")) {
            NSWorkspace.openSubwindow(WindowID.diagReport)
        }

        helpMenu.addSeparator()

        helpMenu.addActionItem("Check for Updates…", icon: systemImage("arrow.down.circle")) {
            [self] in
            updaterController.checkForUpdates(nil)
            NSApp.activate(ignoringOtherApps: true)
        }

        if !vmModel.drmState.isSignedIn {
            menu.addActionItem("Sign In…", icon: systemImage("person.crop.circle")) {
                NSWorkspace.openSubwindow(WindowID.signIn)
            }
        }

        menu.addActionItem("Settings…", shortcut: ",", icon: systemImage("gear")) {
            AppDelegate.shared.showSettingsWindow()
        }

        menu.addActionItem("Quit", shortcut: "q", icon: systemImage("xmark")) { [self] in
            // opt = force quit
            if CGKeyCode.optionKeyPressed {
                AppLifecycle.forceTerminate = true
            }

            // activate in case terminate is blocked by an alert (and so that users can see progress if main window is open)
            NSApp.activate(ignoringOtherApps: true)

            NSApp.terminate(self)
        }
    }

    private func makeContainerItem(
        container: DKContainer,
        showStatus: Bool = false,
        collapseIfStopped: Bool = false
    ) -> NSMenuItem {
        // special case: a stopped item goes in overflow menu and should not have a submenu;
        // only action should be "start"
        if collapseIfStopped && !container.running {
            return newActionItem(container.userName, icon: systemImage("play.fill")) { [self] in
                await actionTracker.with(cid: container.cid, action: .start) {
                    await vmModel.tryDockerContainerStart(container.id)
                }
            }
        }

        let actionInProgress = actionTracker.ongoingFor(container.cid) != nil

        // TODO: highlight container item and open popover
        var icon: NSImage? = nil
        if actionInProgress {
            icon = systemImage("circle.dotted")
        } else if showStatus {
            icon = SystemImages.statusDot(container.statusDot)
        }
        let containerItem = newActionItem(container.userName, icon: icon) { [self] in
            openApp(tab: .dockerContainers)
        }
        let submenu = containerItem.newSubmenu()

        if container.runningOrRestarting {
            submenu.addActionItem(
                "Stop", icon: systemImage("stop.fill"),
                disabled: actionInProgress
            ) { [self] in
                await actionTracker.with(cid: container.cid, action: .stop) {
                    await vmModel.tryDockerContainerStop(container.id)
                }
            }
        } else {
            submenu.addActionItem(
                "Start", icon: systemImage("play.fill"),
                disabled: actionInProgress
            ) { [self] in
                await actionTracker.with(cid: container.cid, action: .start) {
                    await vmModel.tryDockerContainerStart(container.id)
                }
            }
        }

        if container.runningOrRestarting {
            submenu.addActionItem(
                "Restart", icon: systemImage("arrow.clockwise"),
                disabled: actionInProgress
            ) { [self] in
                await actionTracker.with(cid: container.cid, action: .restart) {
                    await vmModel.tryDockerContainerRestart(container.id)
                }
            }
        }

        submenu.addActionItem(
            "Delete", icon: systemImage("trash.fill"),
            disabled: actionInProgress
        ) { [self] in
            await actionTracker.with(cid: container.cid, action: .delete) {
                await vmModel.tryDockerContainerRemove(container.id)
            }
        }

        submenu.addSeparator()

        submenu.addActionItem("Logs", icon: systemImage("doc.text.magnifyingglass")) { [self] in
            container.showLogs(windowTracker: windowTracker)
        }

        submenu.addActionItem(
            "Terminal", icon: systemImage("terminal"), disabled: !container.running
        ) {
            container.openDebugShellFallback()
        }

        submenu.addActionItem("Debug", icon: systemImage("ladybug"), disabled: !container.running) {
            container.openDebugShell()
        }

        submenu.addActionItem("Files", icon: systemImage("folder"), disabled: !container.running) {
            container.openFolder()
        }

        if vmModel.netBridgeAvailable {
            let proto = container.getPreferredProto(vmModel)
            let preferredDomain = container.preferredDomain
            submenu.addActionItem(
                "Open in Browser", icon: systemImage("link"),
                disabled: !container.running || preferredDomain == nil
            ) {
                if let preferredDomain,
                    let url = URL(string: "\(proto)://\(preferredDomain)")
                {
                    NSWorkspace.shared.open(url)
                }
            }
        }

        submenu.addSeparator()

        submenu.addActionItem("ID: \(container.id.prefix(12))", icon: systemImage("doc.on.doc")) {
            NSPasteboard.copy(container.id)
        }

        // in case of pinned hashes
        let truncatedImage =
            container.image.prefix(lineLimit) + (container.image.count > lineLimit ? "…" : "")
        submenu.addActionItem("Image: \(truncatedImage)", icon: systemImage("doc.on.doc")) {
            NSPasteboard.copy(container.image)
        }

        if let ipAddress = container.ipAddress {
            if vmModel.netBridgeAvailable {
                if let domain = container.preferredDomain {
                    submenu.addActionItem("Domain: \(domain)", icon: systemImage("doc.on.doc")) {
                        // copy it. Open in Browser is for opening
                        NSPasteboard.copy(domain)
                    }
                }
            } else {
                submenu.addActionItem("IP: \(ipAddress)", icon: systemImage("doc.on.doc")) {
                    NSPasteboard.copy(ipAddress)
                }
            }
        }

        submenu.addSeparator()

        if !container.ports.isEmpty {
            submenu.addSectionHeader("Ports")
            submenu.addTruncatedItems(container.ports) { port in
                newActionItem(port.formatted) {
                    port.openUrl()
                }
            }
            submenu.addSeparator()
        }
        if !container.mounts.isEmpty {
            submenu.addSectionHeader("Mounts")
            submenu.addTruncatedItems(container.mounts) { mount in
                // formatted w/ arrow is too long usually
                let formatted = mount.formatted
                let mountDesc = formatted.count > lineLimit ? mount.destination : formatted
                return newActionItem(mountDesc) {
                    mount.openSourceDirectory()
                }
            }
            submenu.addSeparator()
        }
        if container.ports.isEmpty && container.mounts.isEmpty {
            submenu.addInfoLine("No Ports or Mounts")
        }

        return containerItem
    }

    private func makeComposeGroupItem(group: ComposeGroup, children: [DockerListItem]) -> NSMenuItem
    {
        // special case: a stopped item goes in overflow menu and should not have a submenu;
        // only action should be "start"
        if !group.anyRunning {
            var icon: NSImage? = nil
            if let a = systemImage("play.fill"),
                let b = systemImage("square.stack.3d.up.fill")
            {
                icon = NSImage.mergeX(a: a, b: b, xPadding: 6)
                icon!.isTemplate = true
            }

            return newActionItem(group.project, icon: icon) { [self] in
                await actionTracker.with(cid: group.cid, action: .start) {
                    await vmModel.tryDockerComposeStart(group.cid)
                }
            }
        }

        let actionInProgress = actionTracker.ongoingFor(group.cid) != nil
        let icon =
            actionInProgress ? systemImage("circle.dotted") : systemImage("square.stack.3d.up.fill")
        let groupItem = newActionItem(group.project, icon: icon) { [self] in
            openApp(tab: .dockerContainers)
        }
        let submenu = groupItem.newSubmenu()
        // supabase cli creates contaners with project label but not service, so Compose doesn't work
        let isFullCompose = group.isFullCompose

        // actions
        if group.anyRunning {
            submenu.addActionItem(
                "Stop", icon: systemImage("stop.fill"),
                disabled: actionInProgress || !isFullCompose
            ) { [self] in
                await actionTracker.with(cid: group.cid, action: .stop) {
                    await vmModel.tryDockerComposeStop(group.cid)
                }
            }
        } else {
            submenu.addActionItem(
                "Start", icon: systemImage("play.fill"),
                disabled: actionInProgress || !isFullCompose
            ) { [self] in
                await actionTracker.with(cid: group.cid, action: .start) {
                    await vmModel.tryDockerComposeStart(group.cid)
                }
            }
        }

        if group.anyRunning {
            submenu.addActionItem(
                "Restart", icon: systemImage("arrow.clockwise"),
                disabled: actionInProgress || !isFullCompose
            ) { [self] in
                await actionTracker.with(cid: group.cid, action: .restart) {
                    await vmModel.tryDockerComposeRestart(group.cid)
                }
            }
        }

        submenu.addActionItem(
            "Delete", icon: systemImage("trash.fill"),
            disabled: actionInProgress || !isFullCompose
        ) { [self] in
            await actionTracker.with(cid: group.cid, action: .delete) {
                await vmModel.tryDockerComposeRemove(group.cid)
            }
        }

        submenu.addSeparator()

        submenu.addActionItem("Logs", icon: systemImage("doc.text.magnifyingglass")) { [self] in
            group.showLogs(windowTracker: windowTracker)
        }

        submenu.addSeparator()

        submenu.addSectionHeader("Services")

        for childItem in children {
            guard case let .container(container) = childItem else {
                continue
            }

            let item = makeContainerItem(container: container, showStatus: true)
            submenu.addItem(item)
        }

        return groupItem
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
        let icon = actionInProgress ? systemImage("circle.dotted") : nil
        let running = record.running  // TODO: check restartingMachines?

        let machineItem = newActionItem(record.name, icon: icon) {
            if record.state != .creating {
                await record.openInTerminal()
            }
        }
        let submenu = machineItem.newSubmenu()

        if running {
            submenu.addActionItem(
                "Stop", icon: systemImage("stop.fill"),
                disabled: actionInProgress
            ) { [self] in
                await actionTracker.with(machine: record, action: .stop) {
                    await vmModel.tryStopContainer(record)
                }
            }
        } else {
            submenu.addActionItem(
                "Start", icon: systemImage("play.fill"),
                disabled: actionInProgress
            ) { [self] in
                await actionTracker.with(machine: record, action: .start) {
                    await vmModel.tryStartContainer(record)
                }
            }
        }

        if running {
            submenu.addActionItem(
                "Restart", icon: systemImage("arrow.clockwise"),
                disabled: actionInProgress
            ) { [self] in
                await actionTracker.with(machine: record, action: .restart) {
                    await vmModel.tryRestartContainer(record)
                }
            }
        }

        // machine delete is too destructive for menu

        submenu.addSeparator()

        submenu.addActionItem(
            "Terminal", icon: systemImage("terminal"), disabled: record.state.isInitializing
        ) {
            await record.openInTerminal()
        }

        submenu.addActionItem(
            "Files", icon: systemImage("folder"), disabled: record.state.isInitializing
        ) {
            record.openNfsDirectory()
        }

        submenu.addSeparator()

        if running {
            let domain = "\(record.name).orb.local"
            submenu.addActionItem(
                "Address: \(domain)", icon: systemImage("doc.on.doc"),
                disabled: !vmModel.netBridgeAvailable
            ) {
                NSPasteboard.copy(domain)
            }
        }

        return machineItem
    }

    private func openApp(tab: NavTabId? = nil) {
        if let tab {
            // set UserDefaults
            Defaults[.selectedTab] = tab
        }

        // open main window
        NSWorkspace.openSubwindow(WindowID.main)
    }

    func onTransitionToBackground() {
        NSLog("onTransitionToBackground")
        // show first-time bg tip? only if onboarding done
        if Defaults[.onboardingCompleted] && !Defaults[.tipsMenubarBgShown] {
            showBgTip()
        }
    }

    private func showBgTip() {
        guard let button = statusItem.button else {
            return
        }
        // don't show duplicate
        guard bgTipPopover == nil else {
            return
        }

        // popover
        let popover = NSPopover()
        popover.contentSize = NSSize(width: 400, height: 500)
        popover.behavior = .applicationDefined
        let contentView = MenuBarTipView(onClose: { [self] in
            self.closeBgTip()
        })
        popover.contentViewController = NSHostingController(rootView: contentView)
        bgTipPopover = popover

        // show
        NSLog("show tip")
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        popover.contentViewController?.view.window?.becomeKey()

        // timeout: 10 sec
        DispatchQueue.main.asyncAfter(deadline: .now() + 10) {
            self.closeBgTip()
        }
    }

    private func closeBgTip() {
        if let popover = bgTipPopover {
            NSLog("close tip")
            popover.close()
            bgTipPopover = nil

            // only update setting if actually closed
            Defaults[.tipsMenubarBgShown] = true
        }
    }
}

extension NSMenu {
    fileprivate func addTruncatedItems<T>(
        _ items: [T],
        overflowHeader: String? = nil,
        overflowItems: [T]? = nil,
        makeItem: (T) -> NSMenuItem?
    ) {
        // limit 5
        for container in items.prefix(maxQuickAccessItems) {
            let item = makeItem(container)
            if let item {
                addItem(item)
            }
        }

        // show extras in submenu
        if items.count > maxQuickAccessItems || overflowItems?.isEmpty == false {
            let submenu = NSMenu()
            let extraItem = NSMenuItem(
                title: "",
                action: nil,
                keyEquivalent: "")
            extraItem.image = systemImage("ellipsis", alt: "More")
            extraItem.submenu = submenu
            addItem(extraItem)

            for container in items.dropFirst(maxQuickAccessItems) {
                let item = makeItem(container)
                if let item {
                    submenu.addItem(item)
                }
            }

            submenu.addSeparator()

            if let overflowItems {
                if let overflowHeader {
                    submenu.addSectionHeader(overflowHeader)
                }

                for container in overflowItems {
                    let item = makeItem(container)
                    if let item {
                        submenu.addItem(item)
                    }
                }
            }
        }
    }

    fileprivate func addInfoLine(_ text: String) {
        let item = NSMenuItem(title: text, action: nil, keyEquivalent: "")
        item.isEnabled = false
        addItem(item)
    }

    fileprivate func addSectionHeader(_ title: String) {
        let item = NSMenuItem()
        // use attributedTitle for emphasis
        item.attributedTitle = NSAttributedString(
            string: title,
            attributes: [
                NSAttributedString.Key.font: NSFont.systemFont(ofSize: 12, weight: .bold),
                NSAttributedString.Key.foregroundColor: NSColor.labelColor,
            ])
        item.isEnabled = false
        addItem(item)
    }

    fileprivate func addSeparator() {
        addItem(NSMenuItem.separator())
    }

    fileprivate func addActionItem(
        _ title: String,
        shortcut: String = "",
        icon: NSImage? = nil,
        disabled: Bool = false,
        action: @escaping () -> Void
    ) {
        addItem(
            newActionItem(
                title,
                shortcut: shortcut,
                icon: icon,
                disabled: disabled,
                action: action))
    }

    fileprivate func addActionItem(
        _ title: String,
        shortcut: String = "",
        icon: NSImage? = nil,
        disabled: Bool = false,
        asyncAction: @escaping () async -> Void
    ) {
        addItem(
            newActionItem(
                title,
                shortcut: shortcut,
                icon: icon,
                disabled: disabled,
                asyncAction: asyncAction))
    }
}

extension NSMenuItem {
    fileprivate func newSubmenu() -> NSMenu {
        let submenu = NSMenu()
        // let us control enable/disable by disabled flag
        submenu.autoenablesItems = false
        self.submenu = submenu
        return submenu
    }
}

private func newActionItem(
    _ title: String,
    shortcut: String = "",
    icon: NSImage? = nil,
    disabled: Bool = false,
    action: @escaping () -> Void
) -> NSMenuItem {
    let controller = ActionItemController(action: action)
    let item = NSMenuItem(
        title: title, action: #selector(controller.action),
        keyEquivalent: shortcut)
    item.target = controller
    item.image = icon
    item.isEnabled = !disabled
    // retain
    item.representedObject = controller
    return item
}

private func newActionItem(
    _ title: String,
    shortcut: String = "",
    icon: NSImage? = nil,
    disabled: Bool = false,
    asyncAction: @escaping () async -> Void
) -> NSMenuItem {
    return newActionItem(
        title,
        shortcut: shortcut,
        icon: icon,
        disabled: disabled
    ) {
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

    @objc func action(_: NSMenuItem) {
        action()
    }
}

private func systemImage(
    _ name: String, bold: Bool = false, small: Bool = false, alt: String? = nil
) -> NSImage? {
    if let image = NSImage(systemSymbolName: name, accessibilityDescription: alt) {
        if bold {
            let config = NSImage.SymbolConfiguration(pointSize: 16, weight: .medium)
            return image.withSymbolConfiguration(config)
        } else if small {
            let config = NSImage.SymbolConfiguration(pointSize: 6, weight: .light, scale: .small)
            // paletteColors
            image.isTemplate = true

            return image.withSymbolConfiguration(config)
        } else {
            return image
        }
    }

    return nil
}

enum SystemImages {
    static func statusDot(isRunning: Bool) -> NSImage {
        statusDot(isRunning ? .green : .red)
    }

    static func statusDot(_ status: StatusDot) -> NSImage {
        let icon = NSImage(named: "MenuBarStatusDot")!
            .tint(color: status.color.withAlphaComponent(0.8))
        icon.size = NSSize(width: 16, height: 16)
        return icon
    }
}

enum StatusDot {
    case green
    case orange
    case red
    case gray

    var color: NSColor {
        switch self {
        case .green:
            return NSColor.systemGreen
        case .orange:
            return NSColor.systemOrange
        case .red:
            return NSColor.systemRed
        case .gray:
            return NSColor.systemGray
        }
    }
}
