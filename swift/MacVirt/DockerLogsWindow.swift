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
                .navigationTitle("Logs: \(container.userName)")
            } else if let composeProject {
                SwiftUILocalProcessTerminal(executable: AppConfig.dockerComposeExe,
                        args: ["-p", composeProject, "logs", "-f"],
                        // env is more robust, user can mess with context
                        env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"])
                .navigationTitle("Project Logs: \(composeProject)")
            } else {
                Text("No container selected")
            }
        }
        .onOpenURL { url in
            if url.pathComponents[1] == "projects" {
                composeProject = url.lastPathComponent
            } else {
                containerId = url.lastPathComponent
            }
        }
        .background(WindowAccessor(holder: windowHolder))
        .onChange(of: windowHolder.window) { window in
            if let window {
                // unrestorable: is ephemeral, and also restored doesn't preserve url
                window.isRestorable = false
            }
        }
    }
}
