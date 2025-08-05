import SwiftUI

struct DockerContainerTerminalTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var useDebugShell = true

    let container: DKContainer

    private var statusBar: some View {
        HStack(alignment: .center) {
            Spacer()

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
        }
        .frame(height: 24)
    }

    var body: some View {
        VStack {
            statusBar

            TerminalTabView(
                command: useDebugShell 
                ? AppConfig.ctlExe + " debug -f \(container.id)" 
                : AppConfig.dockerExe + " exec -it \(container.id) sh -c 'command -v bash > /dev/null && exec bash || exec sh'",
                env: [
                    // env is more robust, user can mess with context
                    "DOCKER_HOST=unix://\(Files.dockerSocket)",
                    // don't show docker debug ads
                    "DOCKER_CLI_HINTS=0",
                ]
            )
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
