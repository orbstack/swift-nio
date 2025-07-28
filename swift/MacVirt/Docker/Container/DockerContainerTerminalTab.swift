import SwiftUI

struct DockerContainerTerminalTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var useDebugShell = true

    let container: DKContainer

    var body: some View {
        TerminalTabView(
            executable: useDebugShell ? AppConfig.ctlExe : AppConfig.dockerExe,
            args: useDebugShell
                ? ["debug", "-f", container.id]
                : [
                    "exec", "-it", container.id, "sh", "-c",
                    "command -v bash > /dev/null && exec bash || exec sh",
                ],
            // env is more robust, user can mess with context
            env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"]
        )
        // banner for toggling Debug Shell
        .overlay(alignment: .topTrailing) {
            Toggle("Debug Shell", isOn: $useDebugShell)
                .toggleStyle(.checkbox)
                .font(.caption)
                .padding(.vertical, 4)
                .padding(.horizontal, 6)
                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
                .overlay(
                    RoundedRectangle(cornerRadius: 8)
                        .stroke(.gray.opacity(0.25), lineWidth: 0.5)
                )
                .padding(4)
        }
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .dockerOpenContainerInNewWindow {
                if useDebugShell {
                    container.openDebugShellFallback()
                } else {
                    container.openInPlainTerminal()
                }
            }
        }
    }
}
