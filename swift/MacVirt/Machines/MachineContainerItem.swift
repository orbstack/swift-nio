//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct MachineContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var listModel: AKListModel

    var record: ContainerRecord
    var selection: Set<String> {
        listModel.selection as! Set<String>
    }

    @State private var presentConfirmDelete = false
    @State private var presentRename = false

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(machine: record) != nil
        let running = record.running || vmModel.restartingMachines.contains(record.id)
        let deletionList = resolveActionList()
        let deleteConfirmMsg =
            deletionList.count > 1 ? "Delete machines?" : "Delete “\(record.name)”?"

        HStack {
            Image("distro_\(record.image.distro)")
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(width: 32, height: 32)
                .padding(.trailing, 8)
                .opacity(running ? 1 : 0.5)
            VStack(alignment: .leading) {
                Text(record.name)
                    .font(.body)
                Text("\(record.image.version), \(record.image.arch)")
                    .font(.subheadline)
                    .foregroundColor(.secondary)
            }
            .opacity(running ? 1 : 0.5)

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

            if running {
                ProgressIconButton(
                    systemImage: "stop.fill",
                    actionInProgress: actionInProgress || record.state == .creating
                ) {
                    finishStop()
                }
                .help("Stop \(record.name)")
            } else {
                ProgressIconButton(
                    systemImage: "play.fill",
                    actionInProgress: actionInProgress || record.state == .creating
                ) {
                    finishStart()
                }
                .help("Start \(record.name)")
            }
        }
        .padding(.vertical, 8)
        .akListContextMenu {
            Group {
                if running {
                    Button(action: {
                        finishStop()
                    }) {
                        Label("Stop", systemImage: "restart")
                    }
                    .disabled(actionInProgress)
                } else {
                    Button(action: {
                        finishRestart()
                    }) {
                        Label("Start", systemImage: "restart")
                    }
                    .disabled(actionInProgress)
                }

                Button(action: {
                    finishRestart()
                }) {
                    Label("Restart", systemImage: "restart")
                }
                .disabled(actionInProgress)
            }

            if selection.count == 1 {
                Divider()

                Button(action: {
                    Task {
                        await record.openInTerminal()
                    }
                }) {
                    Label("Open Terminal", systemImage: "terminal")
                }
                .disabled(record.state == .creating)

                Button(action: {
                    record.openNfsDirectory()
                }) {
                    Label("Open Files", systemImage: "folder")
                }
                .disabled(record.state == .creating)
            }

            Divider()

            Group {
                if selection.count == 1 {
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
                }

                Button(
                    role: .destructive,
                    action: {
                        if CGKeyCode.optionKeyPressed {
                            finishDelete()
                        } else {
                            self.presentConfirmDelete = true
                        }
                    }
                ) {
                    Label("Delete", systemImage: "trash")
                }
                .disabled(actionInProgress)
            }

            if selection.count == 1 {
                Divider()

                Button("Copy Address") {
                    NSPasteboard.copy("\(record.name).orb.local")
                }.disabled(!running || !vmModel.netBridgeAvailable)
            }
        }
        .confirmationDialog(
            deleteConfirmMsg,
            isPresented: $presentConfirmDelete
        ) {
            Button("Delete", role: .destructive) {
                finishDelete()
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .sheet(isPresented: $presentRename) {
            RenameContainerView(name: record.name, record: record, isPresented: $presentRename)
        }
        .akListOnDoubleClick {
            if record.state != .creating {
                Task {
                    await record.openInTerminal()
                }
            }
        }
    }

    @MainActor
    func finishStop() {
        for cid in resolveActionList() {
            let container = (vmModel.containers?.first(where: { $0.id == cid }))!

            Task { @MainActor in
                await actionTracker.with(machine: container.record, action: .delete) {
                    await vmModel.tryStopContainer(container.record)
                }
            }
        }
    }

    @MainActor
    func finishStart() {
        for cid in resolveActionList() {
            let container = (vmModel.containers?.first(where: { $0.id == cid }))!

            Task { @MainActor in
                await actionTracker.with(machine: container.record, action: .delete) {
                    await vmModel.tryStartContainer(container.record)
                }
            }
        }
    }

    @MainActor
    func finishRestart() {
        for cid in resolveActionList() {
            let container = (vmModel.containers?.first(where: { $0.id == cid }))!

            Task { @MainActor in
                await actionTracker.with(machine: container.record, action: .delete) {
                    await vmModel.tryRestartContainer(container.record)
                }
            }
        }
    }

    @MainActor
    func finishDelete() {
        for cid in resolveActionList() {
            let container = (vmModel.containers?.first(where: { $0.id == cid }))!

            Task { @MainActor in
                await actionTracker.with(machine: container.record, action: .delete) {
                    await vmModel.tryDeleteContainer(container.record)
                }
            }
        }
    }

    func isSelected() -> Bool {
        selection.contains(record.id)
    }

    @MainActor
    func resolveActionList() -> Set<String> {
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        if isSelected() {
            return selection
        } else {
            return [record.id]
        }
    }
}
