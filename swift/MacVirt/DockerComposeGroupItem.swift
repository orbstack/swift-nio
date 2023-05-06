//
// Created by Danny Lin on 5/6/23.
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

struct DockerComposeGroupItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var composeGroup: ComposeGroup

    @State private var actionInProgress: DKContainerAction? = nil

    @State private var presentPopover = false

    var body: some View {
        let isRunning = composeGroup.anyRunning

        HStack {
            HStack {
                let color = colors[composeGroup.project.hashValue %% colors.count]
                Image(systemName: "square.stack.3d.up.fill")
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 32, height: 32)
                        .padding(.trailing, 8)
                        .foregroundColor(color)

                VStack(alignment: .leading) {
                    Text(composeGroup.project)
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
                                                //Text(container.status)
                                                //Text(String(container.id.prefix(12)))
                                                  //      .font(.body.monospaced())
                                                //Text(container.image)
                                            }
                                        }
                                                .padding(.leading, 16)
                                    }
                                }
                                .padding(20)
                            }
                }
            }
            .opacity(isRunning ? 1 : 0.5)

            Spacer()

            if isRunning {
                Button(action: {
                    Task { @MainActor in
                        actionInProgress = .stop
                        //await vmModel.tryDockerContainerStop(container.id)
                        actionInProgress = nil
                    }
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
                    Task { @MainActor in
                        actionInProgress = .start
                        //await vmModel.tryDockerContainerStart(container.id)
                        actionInProgress = nil
                    }
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
                Task { @MainActor in
                    actionInProgress = .remove
                    //await vmModel.tryDockerContainerRemove(container.id)
                    actionInProgress = nil
                }
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
                        Task { @MainActor in
                            actionInProgress = .start
                            //await vmModel.tryDockerContainerStart(container.id)
                            actionInProgress = nil
                        }
                    }) {
                        Label("Start", systemImage: "start.fill")
                    }.disabled(actionInProgress != nil || isRunning)

                    Button(action: {
                        Task { @MainActor in
                            actionInProgress = .stop
                            //await vmModel.tryDockerContainerStop(container.id)
                            actionInProgress = nil
                        }
                    }) {
                        Label("Stop", systemImage: "stop.fill")
                    }.disabled(actionInProgress != nil || !isRunning)

                    Button(action: {
                        Task { @MainActor in
                            actionInProgress = .restart
                            //await vmModel.tryDockerContainerRestart(container.id)
                            actionInProgress = nil
                        }
                    }) {
                        Label("Restart", systemImage: "arrow.clockwise")
                    }.disabled(actionInProgress != nil || !isRunning)

                    Button(action: {
                        Task { @MainActor in
                            actionInProgress = .remove
                            //await vmModel.tryDockerContainerRemove(container.id)
                            actionInProgress = nil
                        }
                    }) {
                        Label("Delete", systemImage: "trash.fill")
                    }.disabled(actionInProgress != nil)

                    Divider()

                    Group {
                        Button(action: {
                            let pasteboard = NSPasteboard.general
                            pasteboard.clearContents()
                            pasteboard.setString("xyz", forType: .string)
                        }) {
                            Label("Copy ID", systemImage: "doc.on.doc")
                        }
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
}