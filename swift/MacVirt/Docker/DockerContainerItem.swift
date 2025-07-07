//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

struct DockerContainerImage: View {
    let container: DKContainer

    var body: some View {
        // 32px
        let color = SystemColors.forString(container.userName)
        Image(systemName: "shippingbox.fill")
            .resizable()
            .aspectRatio(contentMode: .fit)
            .frame(width: 16, height: 16)
            .padding(8)
            .foregroundColor(Color(hex: 0xFAFAFA))
            .background(Circle().fill(color))
            // rasterize so opacity works on it as one big image
            .drawingGroup(opaque: false)
    }
}

struct DockerContainerItem: View, Equatable, BaseDockerContainerItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var navModel: MainNavViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var listModel: AKListModel

    @State private var presentConfirmDelete = false

    @Default(.tipsContainerDomainsShow) private var tipsContainerDomainsShow
    @Default(.tipsContainerFilesShow) private var tipsContainerFilesShow

    var container: DKContainer
    var selection: Set<DockerContainerId> {
        listModel.selection as! Set<DockerContainerId>
    }

    var isFirstInList: Bool

    static func == (lhs: DockerContainerItem, rhs: DockerContainerItem) -> Bool {
        lhs.container == rhs.container
    }

    var body: some View {
        let isRunning = container.running
        let actionInProgress = actionTracker.ongoingFor(selfId)

        let deletionList = resolveActionList()
        let deleteConfirmMsg = deletionList.count > 1 ? "Delete containers?" : "Delete container?"

        HStack {
            HStack {
                // make it consistent
                DockerContainerImage(container: container)
                    .padding(.trailing, 8)

                VStack(alignment: .leading) {
                    let nameTxt = container.userName

                    let name = nameTxt.isEmpty ? "(no name)" : nameTxt
                    Text(name)
                        .font(.body)
                        .lineLimit(1)

                    Text(container.image)
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .truncationMode(.tail)
                        .lineLimit(1)
                }
            }
            .opacity(container.running ? 1 : 0.5)
            // padding for expand arrow
            .padding(.leading, 8)

            Spacer()

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
                            Button(action: {
                                tipsContainerDomainsShow = false
                            }) {
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

            if isRunning {
                ProgressIconButton(systemImage: "folder.fill", actionInProgress: false) {
                    container.openFolder()
                }
                .help("Open Files")
                // show domains first, then files
                .if(isFirstInList && !tipsContainerDomainsShow) {
                    // TODO: fix code dupe
                    $0.popover(isPresented: $tipsContainerFilesShow, arrowEdge: .leading) {
                        HStack {
                            Image(systemName: "folder.circle")
                                .resizable()
                                .frame(width: 32, height: 32)
                                .foregroundColor(.accentColor)
                                .padding(.trailing, 4)

                            VStack(alignment: .leading, spacing: 2) {
                                Text("New: Container files in Finder & terminal")
                                    .font(.headline)

                                Text("Easily edit and copy files natively")
                                    .font(.body)
                                    .foregroundColor(.secondary)
                            }
                        }
                        .padding(20)
                        .overlay(alignment: .topLeading) {  // opposite side of arrow edge
                            Button(action: {
                                tipsContainerFilesShow = false
                            }) {
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
                    .help("Stop container")
                } else {
                    ProgressIconButton(
                        systemImage: "play.fill",
                        actionInProgress: actionInProgress?.isStartStop == true
                    ) {
                        finishStart()
                    }
                    .disabled(actionInProgress != nil)
                    .help("Start container")
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
                .help("Delete container")
            }
        }
        .padding(.vertical, 8)
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
                    Button(action: {
                        finishStop()
                    }) {
                        Label("Stop", systemImage: "")
                    }
                    .disabled(actionInProgress != nil || container.isK8s)
                } else {
                    Button(action: {
                        finishStart()
                    }) {
                        Label("Start", systemImage: "")
                    }
                    .disabled(actionInProgress != nil || container.isK8s)
                }

                // allow restart for quick k8s crash testing
                Button(action: {
                    finishRestart()
                }) {
                    Label("Restart", systemImage: "")
                }
                .disabled(actionInProgress != nil || !isRunning)

                Button(action: {
                    presentConfirmDelete = true
                }) {
                    Label("Delete", systemImage: "")
                }
                .disabled(actionInProgress != nil || (container.isK8s && isRunning))

                // allow kill in case k8s container is stuck
                Button(action: {
                    finishKill()
                }) {
                    Label("Kill", systemImage: "")
                }
                .disabled((actionInProgress != nil && actionInProgress != .stop) || !isRunning)
            }

            Divider()

            Group {
                Button(action: {
                    container.showLogs(windowTracker: windowTracker)
                }) {
                    Label("Logs", systemImage: "")
                }

                Button(action: {
                    if vmModel.isLicensed {
                        container.openDebugShell()
                    } else {
                        vmModel.presentRequiresLicense = true
                    }
                }) {
                    Label("Debug Shell", systemImage: "")
                }

                Button(action: {
                    container.openDebugShellFallback()
                }) {
                    Label("Terminal", systemImage: "")
                }
                .disabled(!isRunning)

                Button(action: {
                    container.openFolder()
                }) {
                    Label("Files", systemImage: "")
                }
                .disabled(!isRunning)

                let preferredDomain = container.preferredDomain
                Button(action: {
                    if let preferredDomain,
                        let url = URL(
                            string: "\(container.getPreferredProto(vmModel))://\(preferredDomain)")
                    {
                        NSWorkspace.shared.open(url)
                    }
                }) {
                    Label("Open in Browser", systemImage: "")
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
                    Menu("Ports") {
                        ForEach(container.ports) { port in
                            Button(port.formatted) {
                                port.openUrl()
                            }
                        }
                    }
                }

                if !container.mounts.isEmpty {
                    Menu("Mounts") {
                        ForEach(container.mounts) { mount in
                            Button(mount.formatted) {
                                mount.openSourceDirectory()
                            }
                        }
                    }
                }
            }

            Divider()

            Group {
                Button(action: {
                    NSPasteboard.copy(container.id)
                }) {
                    Label("Copy ID", systemImage: "doc.on.doc")
                }

                Button(action: {
                    NSPasteboard.copy(container.nameOrId)
                }) {
                    Label("Copy Name", systemImage: "doc.on.doc")
                }

                Button(action: {
                    NSPasteboard.copy(container.image)
                }) {
                    Label("Copy Image", systemImage: "doc.on.doc")
                }

                let preferredDomain = container.preferredDomain
                Button(action: {
                    if let preferredDomain {
                        NSPasteboard.copy(preferredDomain)
                    }
                }) {
                    Label("Copy Domain", systemImage: "doc.on.doc")
                }.disabled(!vmModel.netBridgeAvailable || preferredDomain == nil)

                Menu("Copy…") {
                    Button(action: {
                        Task { @MainActor in
                            await container.copyRunCommand()
                        }
                    }) {
                        Label("Command", systemImage: "doc.on.doc")
                    }

                    let ipAddress = container.ipAddress
                    Button(action: {
                        if let ipAddress {
                            NSPasteboard.copy(ipAddress)
                        }
                    }) {
                        Label("IP", systemImage: "doc.on.doc")
                    }.disabled(ipAddress == nil)

                    Button("Path") {
                        NSPasteboard.copy("\(Folders.nfsDockerContainers)/\(container.nameOrId)")
                    }
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

struct ItemRowLabelStyle: LabelStyle {
    @ScaledMetric(relativeTo: .body) var iconWidth = 24

    func makeBody(configuration: Configuration) -> some View {
        HStack {
            configuration.icon
                // set a frame so it lines up
                .frame(width: iconWidth)

            configuration.title
                .padding(.vertical)
                .frame(maxWidth: .infinity, alignment: .leading)

            Image(systemName: "chevron.forward")
                .foregroundColor(.secondary)
                .imageScale(.small)
        }
        .padding(.horizontal)
    }
}

struct ItemRowButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .background {
                Color.secondary.opacity(0.05)
                    .opacity(configuration.isPressed ? 1 : 0)
            }
    }
}
