import SwiftUI

struct MachineLogsTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let machine: ContainerInfo

    var body: some View {
        TerminalTabView(
            executable: AppConfig.ctlExe,
            args: ["logs", machine.id]
        )
    }
}
