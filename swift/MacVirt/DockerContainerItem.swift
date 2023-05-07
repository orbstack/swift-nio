//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

private let colors = [
    Color(.systemRed),
    Color(.systemGreen),
    Color(.systemBlue),
    Color(.systemOrange),
    Color(.systemYellow),
    Color(.systemBrown),
    Color(.systemPink),
    Color(.systemPurple),
    Color(.systemGray),
    Color(.systemTeal),
    Color(.systemIndigo),
    Color(.systemMint),
    Color(.systemCyan),
]

enum DKContainerAction {
    case start
    case stop
    case pause
    case unpause
    case restart
    case remove

    var isStartStop: Bool {
        switch self {
        case .start, .stop, .restart:
            return true
        default:
            return false
        }
    }
}

struct DockerContainerItem: View, Equatable {
    @EnvironmentObject var vmModel: VmViewModel

    var container: DKContainer
    var selection: Set<String>

    @State private var actionInProgress: DKContainerAction? = nil

    @State private var presentPopover = false

    static func == (lhs: DockerContainerItem, rhs: DockerContainerItem) -> Bool {
        lhs.container == rhs.container &&
                lhs.selection == rhs.selection
    }

    var body: some View {
        let isRunning = container.running

        HStack {
            HStack {
                let color = colors[container.id.hashValue %% colors.count]
                Image(systemName: "shippingbox.fill")
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 32, height: 32)
                        .padding(.trailing, 8)
                        .foregroundColor(color)

                VStack(alignment: .leading) {
                    let nameTxt = container.names
                            .map {
                                $0.deletingPrefix("/")
                            }
                            .joined(separator: ", ")
                    let name = nameTxt.isEmpty ? "(no name)" : nameTxt
                    Text(name)
                    .font(.body)
                    .popover(isPresented: $presentPopover, arrowEdge: .trailing) {
                        VStack(alignment: .leading, spacing: 20) {
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Info")
                                        .font(.headline)
                                HStack(spacing: 12) {
                                    VStack(alignment: .trailing) {
                                        Text("Status")
                                        Text("ID")
                                        Text("Image")
                                    }

                                    VStack(alignment: .leading) {
                                        Text(container.status)
                                        Text(String(container.id.prefix(12)))
                                                .font(.body.monospaced())
                                        Text(container.image)
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
                                            Text(formatPort(port))
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
                                                        openPort(port)
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
                                            Text(formatMount(mount))
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
                                                        openMount(mount)
                                                    }
                                        }
                                    }
                                            .padding(.leading, 16)
                                }
                            }

                            if isRunning {
                                VStack(alignment: .leading) {
                                    Button("Open Terminal", action: openInTerminal)

                                    if container.image == "docker/getting-started" {
                                        // special case for more seamless onboarding
                                        Button("Open Tutorial", action: {
                                            NSWorkspace.shared.open(URL(string: "http://localhost")!)
                                        })
                                    }
                                }
                            }
                        }
                                .padding(20)
                    }

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

            if isRunning {
                Button(action: {
                    finishStop()
                }) {
                    let opacity = actionInProgress?.isStartStop == true ? 1.0 : 0.0
                    ZStack {
                        Image(systemName: "stop.fill")
                                .opacity(1 - opacity)

                        ProgressView()
                                .scaleEffect(0.75)
                                .opacity(opacity)
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
                                .scaleEffect(0.75)
                                .opacity(opacity)
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
                            .scaleEffect(0.75)
                            .opacity(opacity)
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
            Button(action: {
                finishStart()
            }) {
                Label("Start", systemImage: "start.fill")
            }.disabled(actionInProgress != nil || isRunning)

            Button(action: {
                finishStop()
            }) {
                Label("Stop", systemImage: "stop.fill")
            }.disabled(actionInProgress != nil || !isRunning)

            Button(action: {
                finishRestart()
            }) {
                Label("Restart", systemImage: "arrow.clockwise")
            }.disabled(actionInProgress != nil || !isRunning)

            Button(action: {
                finishRemove()
            }) {
                Label("Delete", systemImage: "trash.fill")
            }.disabled(actionInProgress != nil)

            Divider()

            Button(action: {
                openInTerminal()
            }) {
                Label("Open Terminal", systemImage: "terminal")
            }.disabled(!isRunning)

            Group {
                if container.ports.isEmpty && container.mounts.isEmpty {
                    Button("No ports or mounts") {}
                            .disabled(true)
                }

                if !container.ports.isEmpty {
                    Menu("Ports") {
                        ForEach(container.ports) { port in
                            Button(formatPort(port)) {
                                openPort(port)
                            }
                        }
                    }
                }

                if !container.mounts.isEmpty {
                    Menu("Mounts") {
                        ForEach(container.mounts) { mount in
                            Button(formatMount(mount)) {
                                openMount(mount)
                            }
                        }
                    }
                }
            }

            Divider()

            Group {
                Button(action: {
                    let pasteboard = NSPasteboard.general
                    pasteboard.clearContents()
                    pasteboard.setString(container.id, forType: .string)
                }) {
                    Label("Copy ID", systemImage: "doc.on.doc")
                }

                Button(action: {
                    let pasteboard = NSPasteboard.general
                    pasteboard.clearContents()
                    pasteboard.setString(container.image, forType: .string)
                }) {
                    Label("Copy Image", systemImage: "doc.on.doc")
                }

                Button(action: {
                    Task { @MainActor in
                        do {
                            let runCmd = try await runProcessChecked(AppConfig.c.dockerExe, ["--context", "orbstack", "inspect", "--format", DKInspectRunCommandTemplate, container.id])

                            let pasteboard = NSPasteboard.general
                            pasteboard.clearContents()
                            pasteboard.setString(runCmd, forType: .string)
                        } catch {
                            NSLog("Failed to get run command: \(error)")
                        }
                    }
                }) {
                    Label("Copy Command", systemImage: "doc.on.doc")
                }
            }
        }
    }

    private func openInTerminal() {
        Task {
            do {
                try await openTerminal(AppConfig.c.dockerExe, ["exec", "-it", container.id, "sh"])
            } catch {
                NSLog("Open terminal failed: \(error)")
            }
        }
    }

    private func formatPort(_ port: DKPort) -> String {
        let ctrPort = port.privatePort
        let localPort = port.publicPort ?? port.privatePort
        let protoSuffix = port.type == "tcp" ? "" : "  (\(port.type.uppercased()))"
        let portStr = ctrPort == localPort ? "\(ctrPort)" : "\(ctrPort) → \(localPort)"

        return "\(portStr)\(protoSuffix)"
    }

    private func openPort(_ port: DKPort) {
        let ctrPort = port.privatePort
        let localPort = port.publicPort ?? port.privatePort
        let httpProto = (ctrPort == 443 || ctrPort == 8443 || localPort == 443 || localPort == 8443) ? "https" : "http"
        NSWorkspace.shared.open(URL(string: "\(httpProto)://localhost:\(localPort)")!)
    }

    private func formatMount(_ mount: DKMountPoint) -> String {
        let src = mount.source
        let dest = mount.destination

        if let volName = mount.name,
           mount.type == .volume {
            return "\(abbreviateMount(volName))  →  \(dest)"
        } else {
            let home = FileManager.default.homeDirectoryForCurrentUser.path
            let prettySrc = src.replacingOccurrences(of: home, with: "~")
            return "\(abbreviateMount(prettySrc))  →  \(dest)"
        }
    }

    private func openMount(_ mount: DKMountPoint) {
        let src = mount.source

        if let volName = mount.name,
           mount.type == .volume {
            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: "\(Folders.nfsDockerVolumes)/\(volName)")
        } else {
            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: src)
        }
    }

    private func abbreviateMount(_ src: String) -> String {
        if src.count > 45 {
            return src.prefix(35) + "…" + src.suffix(10)
        } else {
            return src
        }
    }

    private func finishStop() {
        Task { @MainActor in
            actionInProgress = .stop
            for id in resolveActionList() {
                await vmModel.tryDockerContainerStop(id)
            }
            actionInProgress = nil
        }
    }

    private func finishStart() {
        Task { @MainActor in
            actionInProgress = .start
            for id in resolveActionList() {
                await vmModel.tryDockerContainerStart(id)
            }
            actionInProgress = nil
        }
    }

    private func finishRestart() {
        Task { @MainActor in
            actionInProgress = .restart
            for id in resolveActionList() {
                await vmModel.tryDockerContainerRestart(id)
            }
            actionInProgress = nil
        }
    }

    private func finishRemove() {
        Task { @MainActor in
            actionInProgress = .remove
            for id in resolveActionList() {
                await vmModel.tryDockerContainerRemove(id)
            }
            actionInProgress = nil
        }
    }

    private func isSelected() -> Bool {
        selection.contains(container.id)
    }

    private func resolveActionList() -> Set<String> {
        print("sel is: \(selection)")
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        if isSelected() {
            // SwiftUI List bug: deleted items stay in selection set so we need to filter
            if let containers = vmModel.dockerContainers {
                return selection.filter { sel in
                    containers.contains(where: { $0.id == sel })
                }
            } else {
                return selection
            }
        } else {
            return [container.id]
        }
    }
}