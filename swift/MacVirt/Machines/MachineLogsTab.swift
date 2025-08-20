import SwiftUI

struct MachineLogsTab: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @StateObject private var logsModel = LogsViewModel()

    let machine: ContainerInfo

    var body: some View {
        LogsTabToolbarWrapper {
            LogsView(
                cmdExe: AppConfig.ctlExe,
                args: ["logs", machine.id],
                extraArgs: [],
                extraState: [],
                model: logsModel
            )
        }
    }
}
