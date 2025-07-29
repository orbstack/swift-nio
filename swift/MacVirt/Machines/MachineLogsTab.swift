import SwiftUI

struct MachineLogsTab: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var commandModel = CommandViewModel()
    @StateObject private var logsModel = LogsViewModel()

    let machine: ContainerInfo

    var body: some View {
        LogsView(
            cmdExe: AppConfig.ctlExe,
            args: ["logs", machine.id],
            extraArgs: [],
            extraState: [],
            model: logsModel
        )
        // render under toolbar
        .ignoresSafeArea()
        .environmentObject(commandModel)
    }
}
