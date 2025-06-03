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

    @StateObject private var windowHolder = WindowHolder()

    @State private var presentConfirmDelete = false
    @State private var presentClone = false
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
                    actionInProgress: actionInProgress || record.state.isInitializing
                ) {
                    finishStop()
                }
                .help("Stop \(record.name)")
            } else {
                ProgressIconButton(
                    systemImage: "play.fill",
                    actionInProgress: actionInProgress || record.state.isInitializing
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

            if selection.count <= 1 {
                Divider()

                Button(action: {
                    Task {
                        await record.openInTerminal()
                    }
                }) {
                    Label("Open Terminal", systemImage: "terminal")
                }
                .disabled(record.state.isInitializing)

                Button(action: {
                    record.openNfsDirectory()
                }) {
                    Label("Open Files", systemImage: "folder")
                }
                .disabled(record.state.isInitializing)
            }

            Divider()

            Group {
                if selection.count <= 1 {
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

                    Divider()

                    Button("Clone") {
                        self.presentClone = true
                    }

                    Button("Export") {
                        record.openExportPanel(
                            windowHolder: windowHolder,
                            actionTracker: actionTracker,
                            vmModel: vmModel
                        )
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

            if selection.count <= 1 {
                Divider()

                Button("Copy Domain") {
                    NSPasteboard.copy("\(record.name).orb.local")
                }
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
        .sheet(isPresented: $presentClone) {
            CloneContainerView(name: record.name, record: record, isPresented: $presentClone)
        }
        .sheet(isPresented: $presentRename) {
            RenameContainerView(name: record.name, record: record, isPresented: $presentRename)
        }
        .akListOnDoubleClick {
            if !record.state.isInitializing {
                Task {
                    await record.openInTerminal()
                }
            }
        }
        .windowHolder(windowHolder)
    }

    @MainActor
    func finishStop() {
        for machine in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(machine: machine.record, action: .delete) {
                    await vmModel.tryStopContainer(machine.record)
                }
            }
        }
    }

    @MainActor
    func finishStart() {
        for machine in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(machine: machine.record, action: .delete) {
                    await vmModel.tryStartContainer(machine.record)
                }
            }
        }
    }

    @MainActor
    func finishRestart() {
        for machine in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(machine: machine.record, action: .delete) {
                    await vmModel.tryRestartContainer(machine.record)
                }
            }
        }
    }

    @MainActor
    func finishDelete() {
        for machine in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(machine: machine.record, action: .delete) {
                    await vmModel.tryDeleteContainer(machine.record)
                }
            }
        }
    }

    func isSelected() -> Bool {
        selection.contains(record.id)
    }

    @MainActor
    func resolveActionList() -> [ContainerInfo] {
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        let ids: Set<String> =
            if isSelected() {
                selection
            } else {
                [record.id]
            }

        return ids.compactMap { vmModel.machines?[$0] }
    }
}

extension ContainerRecord {
    func openExportPanel(
        windowHolder: WindowHolder,
        actionTracker: ActionTracker,
        vmModel: VmViewModel
    ) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "\(self.name).tar.zst"

        let window = windowHolder.window ?? NSApp.keyWindow ?? NSApp.windows.first!
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
                let url = panel.url
            {
                Task {
                    await actionTracker.withMachineExport(id: self.id) {
                        await vmModel.tryExportContainer(self, hostPath: url.path)
                    }
                }
            }
        }
    }
}
