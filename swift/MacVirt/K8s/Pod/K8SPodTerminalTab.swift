import SwiftUI

struct K8SPodTerminalTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let pod: K8SPod

    var body: some View {
        TerminalTabView(
            executable: AppConfig.kubectlExe,
            args: pod.terminalArgs,
        )
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .k8sPodOpenInNewWindow {
                pod.openInTerminal()
            }
        }
    }
}
