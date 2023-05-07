//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI

private class SizeHolderModel: ObservableObject {
    @Published var windowSize = CGSize.zero
}

struct DockerLogsWindow: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var windowHolder = WindowHolder()
    @StateObject private var sizeHolderModel = SizeHolderModel()

    @State private var containerId: String?
    @State private var composeProject: String?
    @State private var terminalFrame = CGSize.zero

    var body: some View {
        GeometryReader { geometry in
            VStack {
                if let containerId,
                   let containers = vmModel.dockerContainers,
                   let container = containers.first(where: { $0.id == containerId }) {
                    SwiftUILocalProcessTerminal(executable: AppConfig.dockerExe,
                            args: ["logs", "-f", containerId],
                            // env is more robust, user can mess with context
                            env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"])
                            .padding(8)
                            .navigationTitle("Logs: \(container.userName)")
                            .frame(width: terminalFrame.width, height: terminalFrame.height)
                } else if let composeProject {
                    SwiftUILocalProcessTerminal(executable: AppConfig.dockerComposeExe,
                            args: ["-p", composeProject, "logs", "-f"],
                            // env is more robust, user can mess with context
                            env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"])
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
                        sizeHolderModel.windowSize = geometry.size
                    }
                    .onChange(of: geometry.size) { newSize in
                        sizeHolderModel.windowSize = newSize
                    }
        }
        // match terminal bg
        .background(Color(NSColor.textBackgroundColor))
        .onOpenURL { url in
            if url.pathComponents[1] == "projects" {
                composeProject = url.lastPathComponent
                vmModel.openLogWindowIds.insert(composeProject!)
            } else {
                containerId = url.lastPathComponent
                vmModel.openLogWindowIds.insert(containerId!)
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
        .onReceive(sizeHolderModel.$windowSize.debounce(for: 0.1, scheduler: DispatchQueue.main)) { newSize in
            terminalFrame = newSize
        }
        // effectively make it a throttled leading edge as well
        .onReceive(sizeHolderModel.$windowSize.throttle(for: 0.25, scheduler: DispatchQueue.main, latest: true)) { newSize in
            terminalFrame = newSize
        }
    }
}
