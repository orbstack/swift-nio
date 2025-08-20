//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

private let iconSize: Float = 28
private let statusDotRadius: Float = 6
private let statusMarginRadius: Float = 10

struct DockerContainerItem: View, Equatable, BaseDockerContainerItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var navModel: MainNavViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var listModel: AKListModel

    @State private var presentConfirmDelete = false

    @Default(.tipsContainerDomainsShow) private var tipsContainerDomainsShow
    @Default(.tipsContainerFilesShow) private var tipsContainerFilesShow

    let container: DKContainer
    var selection: Set<DockerContainerId> {
        listModel.selection as! Set<DockerContainerId>
    }

    let isFirstInList: Bool

    static func == (lhs: DockerContainerItem, rhs: DockerContainerItem) -> Bool {
        lhs.container == rhs.container
    }

    var body: some View {
        let isRunning = container.running
        let actionInProgress = actionTracker.ongoingFor(selfId)
        let showStatusDot = container.composeProject != nil

        let deletionList = resolveActionList()
        let deleteConfirmMsg = deletionList.count > 1 ? "Delete containers?" : "Delete container?"

        HStack {
            HStack {
                // make it consistent
                DockerContainerImage(container: container)
                    // mask
                    .mask {
                        Rectangle()
                            .overlay(alignment: .topLeading) {
                                if showStatusDot {
                                    // calculate a position intersecting the circle and y=-x from the bottom-right edge
                                    let x = iconSize - (statusDotRadius / 2)
                                    let y = iconSize - (statusDotRadius / 2)

                                    Circle()
                                        .frame(
                                            width: CGFloat(statusMarginRadius),
                                            height: CGFloat(statusMarginRadius)
                                        )
                                        .position(x: CGFloat(x), y: CGFloat(y))
                                        .blendMode(.destinationOut)
                                }
                            }
                    }
                    // status dot
                    .overlay(alignment: .topLeading) {
                        if showStatusDot {
                            // calculate a position intersecting the circle and y=-x from the bottom-right edge
                            let x = iconSize - (statusDotRadius / 2)
                            let y = iconSize - (statusDotRadius / 2)

                            Circle()
                                .fill(Color(container.statusDot.color).opacity(0.85))
                                .frame(
                                    width: CGFloat(statusDotRadius),
                                    height: CGFloat(statusDotRadius)
                                )
                                .position(x: CGFloat(x), y: CGFloat(y))
                        }
                    }

                VStack(alignment: .leading) {
                    let nameTxt = container.userName

                    let name = nameTxt.isEmpty ? "(no name)" : nameTxt
                    Text(name)
                        .font(.body)
                        .lineLimit(1)

                    Text(container.image)
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        // end of image tag is more important
                        .truncationMode(.head)
                        .lineLimit(1)
                        .help(container.image)
                }
            }
            .opacity(container.running ? 1 : 0.5)
            // padding for expand arrow
            .padding(.leading, 4)

            Spacer()

            ProgressButtonRow {
                if isRunning, let domain = container.preferredDomain {
                    ProgressIconButton(systemImage: "link", actionInProgress: false) {
                        if let url = URL(
                            string: "\(container.getPreferredProto(vmModel))://\(domain)")
                        {
                            NSWorkspace.shared.open(url)
                        }
                    }
                    .help("Open in Browser")
                    .if(isFirstInList) {
                        $0.popover(isPresented: $tipsContainerDomainsShow, arrowEdge: .leading) {
                            HStack {
                                Image(systemName: "network")
                                    .resizable()
                                    .frame(width: 32, height: 32)
                                    .foregroundColor(.accentColor)
                                    .padding(.trailing, 4)

                                VStack(alignment: .leading, spacing: 2) {
                                    Text("New: Domain names for services")
                                        .font(.headline)

                                    Text("See all containers at [orb.local](http://orb.local)")
                                        .font(.body)
                                        .foregroundColor(.secondary)
                                }
                            }
                            .padding(20)
                            .overlay(alignment: .topLeading) {  // opposite side of arrow edge
                                Button {
                                    tipsContainerDomainsShow = false
                                } label: {
                                    Image(systemName: "xmark")
                                        .resizable()
                                        .frame(width: 8, height: 8)
                                        .foregroundColor(.secondary)
                                }
                                .buttonStyle(.plain)
                                .padding(8)
                            }
                        }
                    }
                }

                // don't allow messing with k8s containers -- it'll break things
                if !container.isK8s {
                    if isRunning {
                        ProgressIconButton(
                            systemImage: "stop.fill",
                            actionInProgress: actionInProgress?.isStartStop == true
                        ) {
                            finishStop()
                        }
                        .disabled(actionInProgress != nil)
                        .help("Stop")
                    } else {
                        ProgressIconButton(
                            systemImage: "play.fill",
                            actionInProgress: actionInProgress?.isStartStop == true
                        ) {
                            finishStart()
                        }
                        .disabled(actionInProgress != nil)
                        .help("Start")
                    }
                }

                // allow deleting stopped k8s containers because cri-dockerd leaves phantom ones
                if !(container.isK8s && isRunning) {
                    ProgressIconButton(
                        systemImage: "trash.fill",
                        actionInProgress: actionInProgress == .delete
                    ) {
                        presentConfirmDelete = true
                    }
                    .disabled(actionInProgress != nil)
                    .help("Delete")
                }
            }
        }
        .padding(.vertical, 4)
        .confirmationDialog(
            deleteConfirmMsg,
            isPresented: $presentConfirmDelete
        ) {
            Button("Delete", role: .destructive) {
                finishDelete()
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .akListOnDoubleClick {
            navModel.expandInspector.send()
        }
        .akListContextMenu {
            Group {
                if isRunning {
                    Button {
                        finishStop()
                    } label: {
                        Label("Stop", systemImage: "stop")
                    }
                    .disabled(actionInProgress != nil || container.isK8s)
                } else {
                    Button {
                        finishStart()
                    } label: {
                        Label("Start", systemImage: "play")
                    }
                    .disabled(actionInProgress != nil || container.isK8s)
                }

                // allow restart for quick k8s crash testing
                Button {
                    finishRestart()
                } label: {
                    Label("Restart", systemImage: "arrow.clockwise")
                }
                .disabled(actionInProgress != nil || !isRunning)

                // allow kill in case k8s container is stuck
                Button {
                    finishKill()
                } label: {
                    Label("Kill", systemImage: "xmark.octagon")
                }
                .disabled((actionInProgress != nil && actionInProgress != .stop) || !isRunning)

                if container.paused {
                    Button {
                        finishUnpause()
                    } label: {
                        Label("Unpause", systemImage: "playpause")
                    }
                } else {
                    Button {
                        finishPause()
                    } label: {
                        Label("Pause", systemImage: "pause")
                    }
                }

                Button {
                    presentConfirmDelete = true
                } label: {
                    Label("Delete", systemImage: "trash")
                }
                .disabled(actionInProgress != nil || (container.isK8s && isRunning))
            }

            Divider()

            Group {
                Button {
                    container.showLogs(windowTracker: windowTracker)
                } label: {
                    Label("Logs", systemImage: "doc.text.magnifyingglass")
                }

                Button {
                    if vmModel.isLicensed {
                        container.openDebugShell()
                    } else {
                        vmModel.presentRequiresLicense = true
                    }
                } label: {
                    Label("Debug Shell", systemImage: "ladybug")
                }

                Button {
                    container.openDebugShellFallback()
                } label: {
                    Label("Terminal", systemImage: "terminal")
                }
                .disabled(!isRunning)

                Button {
                    container.openFolder()
                } label: {
                    Label("Files", systemImage: "folder")
                }
                .disabled(!isRunning)

                let preferredDomain = container.preferredDomain
                Button {
                    if let preferredDomain,
                        let url = URL(
                            string: "\(container.getPreferredProto(vmModel))://\(preferredDomain)")
                    {
                        NSWorkspace.shared.open(url)
                    }
                } label: {
                    Label("Open in Browser", systemImage: "link")
                }
                .disabled(!isRunning || !vmModel.netBridgeAvailable || preferredDomain == nil)
            }

            Divider()

            Group {
                if container.ports.isEmpty && container.mounts.isEmpty {
                    Button("No Ports or Mounts") {}
                        .disabled(true)
                }

                if !container.ports.isEmpty {
                    Menu {
                        ForEach(container.ports) { port in
                            Button(port.formatted) {
                                port.openUrl()
                            }
                        }
                    } label: {
                        Label("Ports", systemImage: "network")
                    }
                }

                if !container.mounts.isEmpty {
                    Menu {
                        ForEach(container.mounts) { mount in
                            Button(mount.formatted) {
                                mount.openSourceDirectory()
                            }
                        }
                    } label: {
                        Label("Mounts", systemImage: "externaldrive")
                    }
                }
            }

            Divider()

            Group {
                Button {
                    NSPasteboard.copy(container.id)
                } label: {
                    Label("Copy ID", systemImage: "doc.on.doc")
                }

                Button {
                    NSPasteboard.copy(container.nameOrId)
                } label: {
                    Label("Copy Name", systemImage: "doc.on.doc")
                }

                Button {
                    NSPasteboard.copy(container.image)
                } label: {
                    Label("Copy Image", systemImage: "doc.on.doc")
                }

                let preferredDomain = container.preferredDomain
                Button {
                    if let preferredDomain {
                        NSPasteboard.copy(preferredDomain)
                    }
                } label: {
                    Label("Copy Domain", systemImage: "doc.on.doc")
                }.disabled(!vmModel.netBridgeAvailable || preferredDomain == nil)

                Menu {
                    Button {
                        Task { @MainActor in
                            await container.copyRunCommand()
                        }
                    } label: {
                        Label("Command", systemImage: "doc.on.doc")
                    }

                    let ipAddress = container.ipAddress
                    Button {
                        if let ipAddress {
                            NSPasteboard.copy(ipAddress)
                        }
                    } label: {
                        Label("IP", systemImage: "doc.on.doc")
                    }.disabled(ipAddress == nil)

                    Button {
                        NSPasteboard.copy("\(Folders.nfsDockerContainers)/\(container.nameOrId)")
                    } label: {
                        Label("Path", systemImage: "doc.on.doc")
                    }
                } label: {
                    Label("Copy…", systemImage: "doc.on.doc")
                }
            }
        }
    }

    var selfId: DockerContainerId {
        container.cid
    }
}

protocol BaseDockerContainerItem {
    var vmModel: VmViewModel { get }
    var actionTracker: ActionTracker { get }

    var selection: Set<DockerContainerId> { get }

    var selfId: DockerContainerId { get }

    @MainActor
    func finishStart()
    @MainActor
    func finishStop()
    @MainActor
    func finishPause()
    @MainActor
    func finishUnpause()
    @MainActor
    func finishKill()
    @MainActor
    func finishRestart()
    @MainActor
    func finishDelete()

    func isSelected() -> Bool
    @MainActor
    func resolveActionList() -> Set<DockerContainerId>
}

extension BaseDockerContainerItem {
    @MainActor
    func finishStop() {
        for item in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(cid: item, action: .stop) {
                    switch item {
                    case let .container(id):
                        await vmModel.tryDockerContainerStop(id)
                    case .compose:
                        await vmModel.tryDockerComposeStop(item)
                    default:
                        return
                    }
                }
            }
        }
    }

    @MainActor
    func finishKill() {
        for item in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(cid: item, action: .kill) {
                    switch item {
                    case let .container(id):
                        await vmModel.tryDockerContainerKill(id)
                    case .compose:
                        await vmModel.tryDockerComposeKill(item)
                    default:
                        return
                    }
                }
            }
        }
    }

    @MainActor
    func finishStart() {
        for item in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(cid: item, action: .start) {
                    switch item {
                    case let .container(id):
                        await vmModel.tryDockerContainerStart(id)
                    case .compose:
                        await vmModel.tryDockerComposeStart(item)
                    default:
                        return
                    }
                }
            }
        }
    }

    @MainActor
    func finishPause() {
        for item in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(cid: item, action: .pause) {
                    switch item {
                    case let .container(id):
                        await vmModel.tryDockerContainerPause(id)
                    case .compose:
                        await vmModel.tryDockerComposePause(item)
                    default:
                        return
                    }
                }
            }
        }
    }

    @MainActor
    func finishUnpause() {
        for item in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(cid: item, action: .unpause) {
                    switch item {
                    case let .container(id):
                        await vmModel.tryDockerContainerUnpause(id)
                    case .compose:
                        await vmModel.tryDockerComposeUnpause(item)
                    default:
                        return
                    }
                }
            }
        }
    }

    @MainActor
    func finishRestart() {
        Task { @MainActor in
            for item in resolveActionList() {
                await actionTracker.with(cid: item, action: .restart) {
                    switch item {
                    case let .container(id):
                        await vmModel.tryDockerContainerRestart(id)
                    case .compose:
                        await vmModel.tryDockerComposeRestart(item)
                    default:
                        return
                    }
                }
            }
        }
    }

    @MainActor
    func finishDelete() {
        for item in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(cid: item, action: .delete) {
                    switch item {
                    case let .container(id):
                        await vmModel.tryDockerContainerRemove(id)
                    case .compose:
                        await vmModel.tryDockerComposeRemove(item)
                    default:
                        return
                    }
                }
            }
        }
    }

    func isSelected() -> Bool {
        selection.contains(selfId)
    }

    @MainActor
    func resolveActionList() -> Set<DockerContainerId> {
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        if isSelected() {
            // if we're doing a batch action, we could have groups *and* containers selected
            // in that case, skip containers that are under an existing group to avoid racing
            return selection.filter { sel in
                switch sel {
                case let .container(id):
                    if let container = vmModel.dockerContainers?.byId[id],
                        let composeProject = container.composeProject
                    {
                        return !selection.contains(.compose(project: composeProject))
                    } else {
                        // not a compose project
                        return true
                    }
                default:
                    return true
                }
            }
        } else {
            return [selfId]
        }
    }
}
