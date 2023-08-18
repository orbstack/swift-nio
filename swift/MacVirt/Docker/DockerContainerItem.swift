//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct DockerContainerItem: View, Equatable, BaseDockerContainerItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    @Default(.tipsContainerDomainsShow) private var tipsContainerDomainsShow

    var container: DKContainer
    var selection: Set<DockerContainerId>
    var isFirstInList: Bool

    @State var presentPopover: Bool

    static func == (lhs: DockerContainerItem, rhs: DockerContainerItem) -> Bool {
        lhs.container == rhs.container &&
            lhs.selection == rhs.selection
    }

    var body: some View {
        let isRunning = container.running
        let actionInProgress = actionTracker.ongoingFor(selfId)

        HStack {
            HStack {
                let color = SystemColors.forString(container.id)
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
                ProgressIconButton(systemImage: "info.circle.fill",
                        actionInProgress: false) {
                    presentPopover = true
                }
                .help("Get info")
                .popover(isPresented: $presentPopover, arrowEdge: .leading) {
                    detailsView
                }
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
                        actionInProgress: actionInProgress == .remove) {
                    finishRemove()
                }
                .disabled(actionInProgress != nil)
                .help("Delete container")
            }
        }
        .padding(.vertical, 4)
        .onDoubleClick {
            presentPopover = true
        }
        .contextMenu {
            Group {
                if isRunning {
                    Button(action: {
                        finishStop()
                    }) {
                        Label("Stop", systemImage: "stop.fill")
                    }
                    .disabled(actionInProgress != nil)
                } else {
                    Button(action: {
                        finishStart()
                    }) {
                        Label("Start", systemImage: "start.fill")
                    }
                    .disabled(actionInProgress != nil)
                }

                Button(action: {
                    finishRestart()
                }) {
                    Label("Restart", systemImage: "arrow.clockwise")
                }
                .disabled(actionInProgress != nil || !isRunning)

                Button(action: {
                    finishRemove()
                }) {
                    Label("Delete", systemImage: "trash.fill")
                }
                .disabled(actionInProgress != nil)
            }

            Divider()

            Group {
                Button(action: {
                    presentPopover = true
                }) {
                    Label("Get Info", systemImage: "terminal")
                }

                Button(action: {
                    container.showLogs(vmModel: vmModel)
                }) {
                    Label("Show Logs", systemImage: "terminal")
                }

                Button(action: {
                    container.openInTerminal()
                }) {
                    Label("Open Terminal", systemImage: "terminal")
                }
                .disabled(!isRunning)

                Button(action: {
                    NSWorkspace.shared.open(URL(string: "http://\(container.preferredDomain)")!)
                }) {
                    Label("Open in Browser", systemImage: "terminal")
                }
                .disabled(!isRunning || !vmModel.netBridgeAvailable)
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

                    Button(action: {
                        NSPasteboard.copy(container.preferredDomain)
                    }) {
                        Label("Domain", systemImage: "doc.on.doc")
                    }.disabled(vmModel.config?.networkBridge == false)

                    let ipAddress = container.ipAddresses.first
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
                    let ipAddress = container.ipAddresses.first

                    VStack(alignment: .trailing) {
                        Text("Status")
                        Text("ID")
                        Text("Image")
                        if ipAddress != nil {
                            Text("Address")
                        }
                    }

                    VStack(alignment: .leading) {
                        Text(container.status)
                            .textSelection(.enabled)
                        Text(String(container.id.prefix(12)))
                            .font(.body.monospaced())
                            .textSelection(.enabled)
                        Text(container.image)
                            .textSelection(.enabled)
                        // needs to be running w/ ip to have domain
                        if let ipAddress {
                            if vmModel.netBridgeAvailable {
                                CustomLink(domain, url: URL(string: "http://\(domain)")!)
                            } else {
                                Text(ipAddress)
                                .textSelection(.enabled)
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
                            CustomLink(port.formatted) {
                                port.openUrl()
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
                    if isRunning {
                        Button("Terminal") {
                            container.openInTerminal()
                        }
                    }

                    Button("Logs") {
                        container.showLogs(vmModel: vmModel)
                    }
                }

                if isRunning && container.image == "docker/getting-started" {
                    Spacer()
                            .frame(height: 20)

                    // special case for more seamless onboarding
                    Button("Open Tutorial", action: {
                        NSWorkspace.shared.open(URL(string: "http://localhost")!)
                    })
                }
            }
        }
        .padding(20)
    }

    var selfId: DockerContainerId {
        container.cid
    }
}

private struct CustomLink: View {
    let text: String
    let onClick: () -> Void

    init(_ text: String, onClick: @escaping () -> Void) {
        self.text = text
        self.onClick = onClick
    }

    init(_ text: String, url: URL) {
        self.text = text
        self.onClick = {
            NSWorkspace.shared.open(url)
        }
    }

    var body: some View {
        Text(text)
        .foregroundColor(.blue)
        .onHover { inside in
            if inside {
                NSCursor.pointingHand.push()
            } else {
                NSCursor.pop()
            }
        }
        .onTapGesture {
            onClick()
        }
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
    func finishRestart()
    @MainActor
    func finishRemove()

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
    func finishRemove() {
        for item in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(cid: item, action: .remove) {
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
            // SwiftUI List bug: deleted items stay in selection set so we need to filter
            if let containers = vmModel.dockerContainers {
                let firstPass = selection.filter { sel in
                    switch sel {
                    case .container(let id):
                        return containers.contains(where: { container in container.id == id })
                    case .compose(let project):
                        return containers.contains(where: { container in container.composeProject == project })
                    default:
                        return false
                    }
                }

                // now we only have items that exist
                // if we're doing a batch action, we could have groups *and* containers selected
                // in that case, skip containers that are under an existing group to avoid racing
                return firstPass.filter { sel in
                    switch sel {
                    case .container(let id):
                        if let container = containers.first(where: { container in container.id == id }),
                           let composeProject = container.composeProject {
                            return !firstPass.contains(.compose(project: composeProject))
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