//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI

struct DockerLogsWindow: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var windowHolder = WindowHolder()

    @State private var containerId: String?
    @State private var composeProject: String?

    var body: some View {
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
            } else if let composeProject {
                SwiftUILocalProcessTerminal(executable: AppConfig.dockerComposeExe,
                        args: ["-p", composeProject, "logs", "-f"],
                        // env is more robust, user can mess with context
                        env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"])
                        .padding(8)
                        .navigationTitle("Project Logs: \(composeProject)")
            } else {
                Text("No container selected")
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
        .onChange(of: windowHolder.window) { window in
            if let window {
                // unrestorable: is ephemeral, and also restored doesn't preserve url
                window.isRestorable = false
            }
        }
        .frame(minWidth: 400, minHeight: 200)
    }
}
