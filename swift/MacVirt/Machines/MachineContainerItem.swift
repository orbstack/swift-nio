//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct MachineContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var record: ContainerRecord

    @State private var presentConfirmDelete = false
    @State private var presentRename = false

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(machine: record) != nil

        HStack {
            Image("distro_\(record.image.distro)")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 32, height: 32)
                    .padding(.trailing, 8)
                    .opacity(record.running ? 1 : 0.5)
            VStack(alignment: .leading) {
                Text(record.name)
                        .font(.body)
                Text("\(record.image.version), \(record.image.arch)")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
            }
            .opacity(record.running ? 1 : 0.5)

            Spacer()

            Button(action: {
                record.openNfsDirectory()
            }) {
                Image(systemName: "folder.fill")
                        // match ProgressIconButton size
                .frame(width: 24, height: 24)
            }
            .buttonStyle(.borderless)
            .disabled(actionInProgress)
            .help("Open Files")

            if record.running {
                ProgressIconButton(systemImage: "stop.fill",
                        actionInProgress: actionInProgress || record.state == .creating) {
                    Task { @MainActor in
                        await actionTracker.with(machine: record, action: .stop) {
                            await vmModel.tryStopContainer(record)
                        }
                    }
                }
                .help("Stop \(record.name)")
            } else {
                ProgressIconButton(systemImage: "play.fill",
                        actionInProgress: actionInProgress || record.state == .creating) {
                    Task { @MainActor in
                        await actionTracker.with(machine: record, action: .start) {
                            await vmModel.tryStartContainer(record)
                        }
                    }
                }
                .help("Start \(record.name)")
            }
        }
        .padding(.vertical, 4)
        .contextMenu {
            Group {
                if record.running {
                    Button(action: {
                        Task { @MainActor in
                            await actionTracker.with(machine: record, action: .stop) {
                                await vmModel.tryStopContainer(record)
                            }
                        }
                    }) {
                        Label("Stop", systemImage: "restart")
                    }
                    .disabled(actionInProgress)
                } else {
                    Button(action: {
                        Task { @MainActor in
                            await actionTracker.with(machine: record, action: .start) {
                                await vmModel.tryStartContainer(record)
                            }
                        }
                    }) {
                        Label("Start", systemImage: "restart")
                    }
                    .disabled(actionInProgress)
                }

                Button(action: {
                    Task { @MainActor in
                        await actionTracker.with(machine: record, action: .restart) {
                            await vmModel.tryRestartContainer(record)
                        }
                    }
                }) {
                    Label("Restart", systemImage: "restart")
                }
                .disabled(actionInProgress)
            }

            Divider()

            Button(action: {
                Task {
                    await record.openInTerminal()
                }
            }) {
                Label("Open Terminal", systemImage: "terminal")
            }
            Button(action: {
                record.openNfsDirectory()
            }) {
                Label("Open Files", systemImage: "folder")
            }

            Divider()

            Group {
                Button(action: {
                    Task {
                        await vmModel.trySetDefaultContainer(record)
                    }
                }) {
                    Label("Make Default", systemImage: "star")
                }

                Button("Rename") {
                    self.presentRename = true
                }

                Button(role: .destructive, action: {
                    if CGKeyCode.optionKeyPressed {
                        finishDelete()
                    } else {
                        self.presentConfirmDelete = true
                    }
                }) {
                    Label("Delete", systemImage: "trash")
                }
                .disabled(actionInProgress)
            }

            Divider()

            Button("Copy Address") {
                NSPasteboard.copy("\(record.name).orb.local")
            }.disabled(!record.running || !vmModel.netBridgeAvailable)
        }
        .confirmationDialog("Delete \(record.name)?",
                isPresented: $presentConfirmDelete) {
            Button("Delete", role: .destructive) {
                finishDelete()
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .sheet(isPresented: $presentRename) {
            RenameContainerView(name: record.name, record: record, isPresented: $presentRename)
        }
        .onDoubleClick {
            Task {
                await record.openInTerminal()
            }
        }
    }

    private func finishDelete() {
        Task { @MainActor in
            await actionTracker.with(machine: record, action: .delete) {
                await vmModel.tryDeleteContainer(record)
            }
        }
    }
}