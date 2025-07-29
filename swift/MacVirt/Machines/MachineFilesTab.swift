import SwiftUI

struct MachineFilesTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let machine: ContainerInfo

    var body: some View {
        FileManagerView(rootPath: machine.record.nfsPath)
            // render under toolbar
            .ignoresSafeArea()
            .onReceive(vmModel.toolbarActionRouter) { action in
                if action == .machineOpenInNewWindow {
                    machine.record.openNfsDirectory()
                }
            }
    }
}
