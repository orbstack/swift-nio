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
        .overlay {
            // banner for toggling Debug Shell
        }
    }
}
