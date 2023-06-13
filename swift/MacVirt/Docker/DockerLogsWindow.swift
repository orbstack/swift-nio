//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI
import Combine

// equal to swiftterm scrollback
private let maxLines = 5000

class TerminalViewModel: ObservableObject {
    @Published var windowSize = CGSize.zero

    let clearCommand = PassthroughSubject<(), Never>()
    let copyAllCommand = PassthroughSubject<(), Never>()
}

struct DockerLogsWindow: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var windowHolder = WindowHolder()
    @StateObject private var terminalModel = TerminalViewModel()

    @State private var containerId: String?
    @State private var composeProject: String?
    @State private var terminalFrame = CGSize.zero

    // persist if somehow window gets restored
    @SceneStorage("DockerLogs_url") private var savedUrl: URL?

    private var terminal: SwiftUILocalProcessTerminal!

    var body: some View {
        GeometryReader { geometry in
            VStack {
                if let containerId,
                   let containers = vmModel.dockerContainers,
                   let container = containers.first(where: { $0.id == containerId }) {
                    SwiftUILocalProcessTerminal(executable: AppConfig.dockerExe,
                            args: ["logs", "-f", "-n", String(maxLines), containerId],
                            // env is more robust, user can mess with context
                            env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"],
                            model: terminalModel)
                    .padding(8)
                    .navigationTitle("Logs: \(container.userName)")
                    .frame(width: terminalFrame.width, height: terminalFrame.height)
                } else if let composeProject {
                    SwiftUILocalProcessTerminal(executable: AppConfig.dockerComposeExe,
                            args: ["-p", composeProject, "logs", "-f", "-n", String(maxLines)],
                            // env is more robust, user can mess with context
                            env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"],
                            model: terminalModel)
                    .padding(8)
                    .navigationTitle("Project Logs: \(composeProject)")
                    .frame(width: terminalFrame.width, height: terminalFrame.height)
                } else {
                    Spacer()
                    HStack {
                        Spacer()
                        Text("No container selected")
                        Spacer()
                    }
                    Spacer()
                }
            }
            .onAppear {
                terminalModel.windowSize = geometry.size
            }
            .onChange(of: geometry.size) { newSize in
                terminalModel.windowSize = newSize
            }
        }
        // match terminal bg
        .background(Color(NSColor.textBackgroundColor))
        .onOpenURL { url in
            onOpenURL(url)
        }
        .task {
            if let savedUrl {
                onOpenURL(savedUrl)
            }
        }
        .onDisappear {
            if let containerId {
                vmModel.openLogWindowIds.remove(containerId)
            } else if let composeProject {
                vmModel.openLogWindowIds.remove(composeProject)
            }
        }
        .background(WindowAccessor(holder: windowHolder))
        .onAppear {
            if let window = windowHolder.window {
                window.isRestorable = false
            }
        }
        .onChange(of: windowHolder.window) { window in
            if let window {
                // unrestorable: is ephemeral, and also restored doesn't preserve url
                window.isRestorable = false
            }
        }
        .frame(minWidth: 400, minHeight: 200)
        // debounce terminal resize for perf
        .onReceive(terminalModel.$windowSize.debounce(for: 0.1, scheduler: DispatchQueue.main)) { newSize in
            terminalFrame = newSize
        }
        // effectively make it a throttled leading edge as well
        .onReceive(terminalModel.$windowSize.throttle(for: 0.25, scheduler: DispatchQueue.main, latest: true)) { newSize in
            terminalFrame = newSize
        }
        // clear toolbar
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Button(action: {
                    terminalModel.copyAllCommand.send(())
                }) {
                    Label("Copy", systemImage: "doc.on.doc")
                }
                .disabled(containerId == nil && composeProject == nil)
                .help("Copy")
                .keyboardShortcut("c", modifiers: [.command, .shift])
            }

            ToolbarItem(placement: .automatic) {
                Button(action: {
                    terminalModel.clearCommand.send(())
                }) {
                    Label("Clear", systemImage: "clear")
                }
                .disabled(containerId == nil && composeProject == nil)
                .help("Clear")
                .keyboardShortcut("k", modifiers: [.command])
            }
        }
    }

    private func onOpenURL(_ url: URL) {
        if url.pathComponents[1] == "project-logs" {
            composeProject = url.lastPathComponent
            vmModel.openLogWindowIds.insert(composeProject!)
        } else {
            containerId = url.lastPathComponent
            vmModel.openLogWindowIds.insert(containerId!)
        }
        savedUrl = url
    }
}
