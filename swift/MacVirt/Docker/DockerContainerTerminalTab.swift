import SwiftUI

struct DockerContainerTerminalTab: View {
    @StateObject private var terminalModel = TerminalViewModel()

    @State private var useDebugShell = true

    let container: DKContainer

    var body: some View {
        // TODO: focus-on-appear
        SwiftUILocalProcessTerminal(
            executable: useDebugShell ? AppConfig.ctlExe : AppConfig.dockerExe,
            args: useDebugShell ? ["debug", "-f", container.id] : ["exec", "-it", container.id, "sh", "-c", "command -v bash > /dev/null && exec bash || exec sh"],
            // env is more robust, user can mess with context
            env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"],
            model: terminalModel
        )
        // padding that matches terminal bg color
        .padding(4)
        .background(Color(NSColor.textBackgroundColor))
        // otherwise terminal leaks behind toolbar when scrolled
        .clipped()
        // banner for toggling Debug Shell
        .overlay(alignment: .topTrailing) {
            Toggle("Debug Shell", isOn: $useDebugShell)
                .toggleStyle(.checkbox)
            .padding(.vertical, 8)
            .padding(.horizontal, 12)
            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .stroke(.gray.opacity(0.25), lineWidth: 0.5)
            )
            .padding(4)
        }
    }
}
