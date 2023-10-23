//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct DockerContainerItem: View, Equatable, BaseDockerContainerItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var listModel: AKListModel

    @Default(.tipsContainerDomainsShow) private var tipsContainerDomainsShow

    var container: DKContainer
    var selection: Set<DockerContainerId> {
        listModel.selection as! Set<DockerContainerId>
    }
    var isFirstInList: Bool

    @State private var presentPopover = false

    static func == (lhs: DockerContainerItem, rhs: DockerContainerItem) -> Bool {
        lhs.container == rhs.container
    }

    var body: some View {
        let isRunning = container.running
        let actionInProgress = actionTracker.ongoingFor(selfId)

        HStack {
            HStack {
                // make it consistent
                let color = SystemColors.forString(container.userName)
                Image(systemName: "shippingbox.fill")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 16, height: 16)
                    .padding(8)
                    .foregroundColor(Color(hex: 0xfafafa))
                    .background(Circle().fill(color))
                    // rasterize so opacity works on it as one big image
                    .drawingGroup(opaque: true)
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

            // crash on macOS 12 without nested HStack
            HStack {
                if isRunning, let domain = container.preferredDomain {
                    ProgressIconButton(systemImage: "link",
                            actionInProgress: false) {
                        if let url = URL(string: "http://\(domain)") {
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
                            .overlay(alignment: .topLeading) { // opposite side of arrow edge
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

                ProgressIconButton(systemImage: "info.circle.fill",
                        actionInProgress: false) {
                    presentPopover = true
                }
                .help("Get info")
                .popover(isPresented: $presentPopover, arrowEdge: .leading) {
                    detailsView
                }

                if isRunning {
                    ProgressIconButton(systemImage: "stop.fill",
                            actionInProgress: actionInProgress?.isStartStop == true) {
                        finishStop()
                    }
                    .disabled(actionInProgress != nil)
                    .help("Stop container")
                } else {
                    ProgressIconButton(systemImage: "play.fill",
                            actionInProgress: actionInProgress?.isStartStop == true) {
                        finishStart()
                    }
                    .disabled(actionInProgress != nil)
                    .help("Start container")
                }

                ProgressIconButton(systemImage: "trash.fill",
                        actionInProgress: actionInProgress == .delete) {
                    finishDelete()
                }
                .disabled(actionInProgress != nil)
                .help("Delete container")
            }
        }
        .padding(.vertical, 8)
        .akListOnDoubleClick {
            presentPopover = true
        }
        .akListContextMenu {
            Group {
                if isRunning {
                    Button(action: {
                        finishStop()
                    }) {
                        Label("Stop", systemImage: "")
                    }
                    .disabled(actionInProgress != nil)
                } else {
                    Button(action: {
                        finishStart()
                    }) {
                        Label("Start", systemImage: "")
                    }
                    .disabled(actionInProgress != nil)
                }

                Button(action: {
                    finishRestart()
                }) {
                    Label("Restart", systemImage: "")
                }
                .disabled(actionInProgress != nil || !isRunning)

                Button(action: {
                    finishDelete()
                }) {
                    Label("Delete", systemImage: "")
                }
                .disabled(actionInProgress != nil)

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
                    presentPopover = true
                }) {
                    Label("Get Info", systemImage: "terminal")
                }

                Button(action: {
                    container.showLogs(windowTracker: windowTracker)
                }) {
                    Label("Show Logs", systemImage: "terminal")
                }

                Button(action: {
                    container.openInTerminal()
                }) {
                    Label("Open Terminal", systemImage: "terminal")
                }
                .disabled(!isRunning)

                let preferredDomain = container.preferredDomain
                Button(action: {
                    if let preferredDomain,
                       let url = URL(string: "http://\(preferredDomain)") {
                        NSWorkspace.shared.open(url)
                    }
                }) {
                    Label("Open in Browser", systemImage: "terminal")
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
                Menu("Copy") {
                    Button(action: {
                        NSPasteboard.copy(container.id)
                    }) {
                        Label("ID", systemImage: "doc.on.doc")
                    }

                    Button(action: {
                        NSPasteboard.copy(container.image)
                    }) {
                        Label("Image", systemImage: "doc.on.doc")
                    }

                    Button(action: {
                        Task { @MainActor in
                            await container.copyRunCommand()
                        }
                    }) {
                        Label("Command", systemImage: "doc.on.doc")
                    }

                    let preferredDomain = container.preferredDomain
                    Button(action: {
                        if let preferredDomain {
                            NSPasteboard.copy(preferredDomain)
                        }
                    }) {
                        Label("Domain", systemImage: "doc.on.doc")
                    }.disabled(!vmModel.netBridgeAvailable || preferredDomain == nil)

                    let ipAddress = container.ipAddress
                    Button(action: {
                        if let ipAddress {
                            NSPasteboard.copy(ipAddress)
                        }
                    }) {
                        Label("IP", systemImage: "doc.on.doc")
                    }.disabled(ipAddress == nil)
                }
            }
        }
    }

    private var detailsView: some View {
        VStack(alignment: .leading, spacing: 20) {
            let isRunning = container.running

            VStack(alignment: .leading, spacing: 4) {
                Text("Info")
                        .font(.headline)
                HStack(spacing: 12) {
                    let domain = container.preferredDomain
                    let ipAddress = container.ipAddress

                    VStack(alignment: .trailing, spacing: 2) {
                        Text("Status")
                        Text("ID")
                        Text("Image")
                        if ipAddress != nil {
                            Text("Address")
                        }
                    }

                    VStack(alignment: .leading, spacing: 2) {
                        Text(container.status)
                        CopyableText(String(container.id.prefix(12)), copyAs: container.id)
                            .font(.body.monospaced())
                        CopyableText(container.image)
                        // needs to be running w/ ip to have domain
                        if let ipAddress,
                           let domain,
                           let url = URL(string: "http://\(domain)") {
                            if vmModel.netBridgeAvailable {
                                CopyableText(copyAs: domain) {
                                    CustomLink(domain, url: url)
                                }
                            } else {
                                CopyableText(ipAddress)
                            }
                        }
                    }
                }
                .padding(.leading, 16)
            }

            if !container.ports.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Ports")
                            .font(.headline)
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(container.ports) { port in
                            CopyableText(copyAs: "\(port.localPort)") {
                                CustomLink(port.formatted) {
                                    port.openUrl()
                                }
                            }
                        }
                    }
                    .padding(.leading, 16)
                }
            }

            if !container.mounts.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Mounts")
                        .font(.headline)
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(container.mounts) { mount in
                            CustomLink(mount.formatted) {
                                mount.openSourceDirectory()
                            }
                        }
                    }
                    .padding(.leading, 16)
                }
            }

            VStack(alignment: .leading) {
                HStack {
                    Button {
                        container.showLogs(windowTracker: windowTracker)
                    } label: {
                        Label("Logs", systemImage: "doc.text.magnifyingglass")
                    }
                    .controlSize(.large)

                    if isRunning {
                        Button {
                            container.openInTerminal()
                        } label: {
                            Label("Terminal", systemImage: "terminal")
                        }
                        .controlSize(.large)
                    }
                }

                if isRunning && container.image == "docker/getting-started" {
                    Spacer()
                            .frame(height: 20)

                    // special case for more seamless onboarding
                    Button {
                        NSWorkspace.shared.open(URL(string: "http://localhost")!)
                    } label: {
                        Label("Open Tutorial", systemImage: "questionmark.circle")
                    }
                    .controlSize(.large)
                }
            }
        }
        .padding(20)
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
                    case .container(let id):
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
                    case .container(let id):
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
                    case .container(let id):
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
                    case .container(let id):
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
                    case .container(let id):
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
            if let containers = vmModel.dockerContainers {
                // if we're doing a batch action, we could have groups *and* containers selected
                // in that case, skip containers that are under an existing group to avoid racing
                return selection.filter { sel in
                    switch sel {
                    case .container(let id):
                        if let container = containers.first(where: { container in container.id == id }),
                           let composeProject = container.composeProject {
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
                return selection
            }
        } else {
            return [selfId]
        }
    }
}