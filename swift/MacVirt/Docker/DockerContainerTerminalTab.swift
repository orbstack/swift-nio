import SwiftUI

struct DockerContainerTerminalTab: View {
    @StateObject private var terminalModel = TerminalViewModel()

    let container: DKContainer

    var body: some View {
        SwiftUILocalProcessTerminal(
            executable: AppConfig.dockerExe,
            args: ["exec", "-it", container.id, "sh", "-c", "command -v bash > /dev/null && exec bash || exec sh"],
            // env is more robust, user can mess with context
            env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"],
            model: terminalModel
        )
        // padding that matches terminal bg color
        .padding(4)
        .background(Color(NSColor.textBackgroundColor))
        // otherwise terminal leaks behind toolbar when scrolled
        .clipped()
        .overlay {
            // banner for toggling Debug Shell
        }
    }
}
