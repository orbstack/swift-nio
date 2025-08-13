import SwiftUI

struct MachineTerminalTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    let machine: ContainerInfo

    var body: some View {
        Group {
            if machine.record.state == .running {
                TerminalView(
                    command: [AppConfig.ctlExe, "run", "-m", machine.id]
                )
            } else {
                VStack(spacing: 16) {  // match ContentUnavailableViewCompat desc padding
                    ContentUnavailableViewCompat(
                        "Machine Not Running", systemImage: "moon.zzz.fill")

                    Button {
                        Task {
                            await vmModel.tryStartContainer(machine.record)
                        }
                    } label: {
                        Text("Start")
                            .padding(.horizontal, 4)
                    }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .controlSize(.large)
                }
                .padding(16)
            }
        }
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .machineOpenInNewWindow {
                Task {
                    await machine.record.openInTerminal()
                }
            }
        }
    }
}
