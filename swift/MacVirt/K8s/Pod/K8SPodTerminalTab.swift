import SwiftUI

struct K8SPodTerminalTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let pod: K8SPod

    var body: some View {
        if pod.uiState == .running {
            TerminalView(
                command: AppConfig.kubectlExe + " " + pod.terminalArgs.joined(separator: " "),
            )
            .onReceive(vmModel.toolbarActionRouter) { action in
                if action == .k8sPodOpenInNewWindow {
                    pod.openInTerminal()
                }
            }
        } else {
            ContentUnavailableViewCompat(
                "Pod Not Running", systemImage: "moon.zzz.fill"
            )
            .padding(16)
        }
    }
}
