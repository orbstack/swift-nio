import SwiftUI

struct MachineTerminalTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let machine: ContainerInfo

    var body: some View {
        TerminalTabView(
            executable: AppConfig.ctlExe,
            args: ["run", "-m", machine.id]
        )
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .machineOpenInNewWindow {
                Task {
                    await machine.record.openInTerminal()
                }
            }
        }
    }
}
