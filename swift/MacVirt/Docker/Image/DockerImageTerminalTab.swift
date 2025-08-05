import SwiftUI

struct DockerImageTerminalTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let image: DKSummaryAndFullImage

    var body: some View {
        TerminalTabView(
            command: AppConfig.ctlExe + " debug -f \(image.id)",
            // env is more robust, user can mess with context
            env: ["DOCKER_HOST=unix://\(Files.dockerSocket)"]
        )
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .dockerOpenImageInNewWindow {
                image.summary.openDebugShell()
            }
        }
    }
}
