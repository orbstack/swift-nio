//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerContainerItem: View, Equatable, BaseDockerContainerItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var container: DKContainer
    var selection: Set<DockerContainerId>

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
                let color = SystemColors.forHashable(container.id)
                Image(systemName: "shippingbox.fill")
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 32, height: 32)
                        .padding(.trailing, 8)
                        .foregroundColor(color)

                VStack(alignment: .leading) {
                    let nameTxt = container.userName

                    let name = nameTxt.isEmpty ? "(no name)" : nameTxt
                    Text(name)
                    .font(.body)

                    let shortId = String(container.id.prefix(12))
                    Text("\(shortId) (\(container.image))")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                            .truncationMode(.tail)
                            .lineLimit(1)
                }
            }
            .opacity(container.running ? 1 : 0.5)

            Spacer()

            Button(action: {
                presentPopover = true
            }) {
                ZStack {
                    Image(systemName: "info.circle.fill")

                    // as padding to match size
                    ProgressView()
                            .scaleEffect(0.7)
                            .opacity(0)
                            .frame(maxWidth: 32.142857*0.7, maxHeight: 32.142857*0.7)
                }
            }
                    .buttonStyle(.borderless)
                    .help("Get info")
                    .popover(isPresented: $presentPopover, arrowEdge: .leading) {
                        detailsView
                    }

            if isRunning {
                Button(action: {
                    finishStop()
                }) {
                    let opacity = actionInProgress?.isStartStop == true ? 1.0 : 0.0
                    ZStack {
                        Image(systemName: "stop.fill")
                                .opacity(1 - opacity)

                        ProgressView()
                                .scaleEffect(0.7)
                                .opacity(opacity)
                                .frame(maxWidth: 32.142857*0.7, maxHeight: 32.142857*0.7)
                    }
                }
                        .buttonStyle(.borderless)
                        .disabled(actionInProgress != nil)
                        .help("Stop container")
            } else {
                Button(action: {
                    finishStart()
                }) {
                    let opacity = actionInProgress?.isStartStop == true ? 1.0 : 0.0
                    ZStack {
                        Image(systemName: "play.fill")
                                .opacity(1 - opacity)

                        ProgressView()
                                .scaleEffect(0.7)
                                .opacity(opacity)
                                .frame(maxWidth: 32.142857*0.7, maxHeight: 32.142857*0.7)
                    }
                }
                        .buttonStyle(.borderless)
                        .disabled(actionInProgress != nil)
                        .help("Start container")
            }

            Button(action: {
                finishRemove()
            }) {
                let opacity = actionInProgress == .remove ? 1.0 : 0.0
                ZStack {
                    Image(systemName: "trash.fill")
                            .opacity(1 - opacity)

                    ProgressView()
                            .scaleEffect(0.7)
                            .opacity(opacity)
                            .frame(maxWidth: 32.142857*0.7, maxHeight: 32.142857*0.7)
                }
            }
                    .buttonStyle(.borderless)
                    .disabled(actionInProgress != nil)
                    .help("Delete container")
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
                    let ipAddress = container.ipAddresses.first

                    VStack(alignment: .trailing) {
                        Text("Status")
                        Text("ID")
                        Text("Image")
                        if ipAddress != nil {
                            Text("IP")
                        }
                    }

                    VStack(alignment: .leading) {
                        Text(container.status)
                        Text(String(container.id.prefix(12)))
                                .font(.body.monospaced())
                        Text(container.image)
                        if let ipAddress {
                            Text(ipAddress)
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
                            Text(port.formatted)
                                    .font(.body.monospacedDigit())
                                    .foregroundColor(.blue)
                                    .onHover { inside in
                                        if inside {
                                            NSCursor.pointingHand.push()
                                        } else {
                                            NSCursor.pop()
                                        }
                                    }
                                    .onTapGesture {
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
                            Text(mount.formatted)
                                    .font(.body.monospacedDigit())
                                    .foregroundColor(.blue)
                                    .onHover { inside in
                                        if inside {
                                            NSCursor.pointingHand.push()
                                        } else {
                                            NSCursor.pop()
                                        }
                                    }
                                    .onTapGesture {
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
        .container(id: container.id)
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
                actionTracker.begin(item, action: .stop)

                switch item {
                case .container(let id):
                    await vmModel.tryDockerContainerStop(id)
                case .compose:
                    await vmModel.tryDockerComposeStop(item)
                default:
                    return
                }

                actionTracker.end(item)
            }
        }
    }

    @MainActor
    func finishStart() {
        for item in resolveActionList() {
            Task { @MainActor in
                actionTracker.begin(item, action: .start)

                switch item {
                case .container(let id):
                    await vmModel.tryDockerContainerStart(id)
                case .compose:
                    await vmModel.tryDockerComposeStart(item)
                default:
                    return
                }

                actionTracker.end(item)
            }
        }
    }

    @MainActor
    func finishRestart() {
        Task { @MainActor in
            for item in resolveActionList() {
                actionTracker.begin(item, action: .restart)

                switch item {
                case .container(let id):
                    await vmModel.tryDockerContainerRestart(id)
                case .compose:
                    await vmModel.tryDockerComposeRestart(item)
                default:
                    return
                }

                actionTracker.end(item)
            }
        }
    }

    @MainActor
    func finishRemove() {
        for item in resolveActionList() {
            Task { @MainActor in
                actionTracker.begin(item, action: .remove)

                switch item {
                case .container(let id):
                    await vmModel.tryDockerContainerRemove(id)
                case .compose:
                    await vmModel.tryDockerComposeRemove(item)
                default:
                    return
                }

                actionTracker.end(item)
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
                    case .compose(let project, _):
                        return containers.contains(where: { container in container.labels[DockerLabels.composeProject] == project })
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
                           let composeProject = container.labels[DockerLabels.composeProject],
                           let configFiles = container.labels[DockerLabels.composeConfigFiles] {
                            return !firstPass.contains(.compose(project: composeProject, configFiles: configFiles))
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