//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct MachineContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var listModel: AKListModel

    var info: ContainerInfo
    var selection: Set<String> {
        listModel.selection as! Set<String>
    }

    @StateObject private var windowHolder = WindowHolder()

    @State private var presentConfirmDelete = false
    @State private var presentClone = false
    @State private var presentRename = false

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(machine: info.record) != nil
        let running = info.record.running || vmModel.restartingMachines.contains(info.record.id)
        let deletionList = resolveActionList()
        let deleteConfirmMsg =
            deletionList.count > 1 ? "Delete machines?" : "Delete “\(info.record.name)”?"

        HStack {
            Image("distro_\(info.record.image.distro)")
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(width: 32, height: 32)
                .padding(.trailing, 8)
                .opacity(running ? 1 : 0.5)
            VStack(alignment: .leading) {
                Text(info.record.name)
                    .font(.body)
                Text("\(info.record.image.version), \(info.record.image.arch)")
                    .font(.subheadline)
                    .foregroundColor(.secondary)
            }
            .opacity(running ? 1 : 0.5)

            Spacer()

            Button {
                info.record.openNfsDirectory()
            } label: {
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
                    actionInProgress: actionInProgress || info.record.state.isInitializing
                ) {
                    finishStop()
                }
                .help("Stop \(info.record.name)")
            } else {
                ProgressIconButton(
                    systemImage: "play.fill",
                    actionInProgress: actionInProgress || info.record.state.isInitializing
                ) {
                    finishStart()
                }
                .help("Start \(info.record.name)")
            }
        }
        .padding(.vertical, 8)
        .akListContextMenu {
            Group {
                if running {
                    Button {
                        finishStop()
                    } label: {
                        Label("Stop", systemImage: "stop")
                    }
                    .disabled(actionInProgress)
                } else {
                    Button {
                        finishRestart()
                    } label: {
                        Label("Start", systemImage: "play")
                    }
                    .disabled(actionInProgress)
                }

                Button {
                    finishRestart()
                } label: {
                    Label("Restart", systemImage: "arrow.clockwise")
                }
                .disabled(actionInProgress)
            }

            if selection.count <= 1 {
                Divider()

                Button {
                    Task {
                        await info.record.openInTerminal()
                    }
                } label: {
                    Label("Terminal", systemImage: "terminal")
                }
                .disabled(info.record.state.isInitializing)

                Button {
                    info.record.openNfsDirectory()
                } label: {
                    Label("Files", systemImage: "folder")
                }
                .disabled(info.record.state.isInitializing)
            }

            Divider()

            Group {
                if selection.count <= 1 {
                    Button {
                        Task {
                            await vmModel.trySetDefaultContainer(info.record)
                        }
                    } label: {
                        Label("Make Default", systemImage: "star")
                    }

                    Button {
                        self.presentRename = true
                    } label: {
                        Label("Rename", systemImage: "pencil")
                    }

                    Divider()

                    Button {
                        self.presentClone = true
                    } label: {
                        Label("Clone", systemImage: "plus.square.on.square")
                    }

                    Button {
                        info.record.openExportPanel(
                            windowHolder: windowHolder,
                            actionTracker: actionTracker,
                            vmModel: vmModel
                        )
                    } label: {
                        Label("Export", systemImage: "square.and.arrow.up")
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

                Button {
                    NSPasteboard.copy("\(info.record.name).orb.local")
                } label: {
                    Label("Copy Domain", systemImage: "doc.on.doc")
                }

                Button {
                    if let ip4 = info.ip4 {
                        NSPasteboard.copy(ip4)
                    }
                } label: {
                    Label("Copy IPv4", systemImage: "doc.on.doc")
                }.disabled(info.ip4 == nil)

                Button {
                    if let ip6 = info.ip6 {
                        NSPasteboard.copy(ip6)
                    }
                } label: {
                    Label("Copy IPv6", systemImage: "doc.on.doc")
                }.disabled(info.ip6 == nil)
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
            CloneContainerView(
                name: info.record.name, record: info.record, isPresented: $presentClone)
        }
        .sheet(isPresented: $presentRename) {
            RenameContainerView(
                name: info.record.name, record: info.record, isPresented: $presentRename)
        }
        .akListOnDoubleClick {
            if !info.record.state.isInitializing {
                Task {
                    await info.record.openInTerminal()
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
        selection.contains(info.record.id)
    }

    @MainActor
    func resolveActionList() -> [ContainerInfo] {
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        let ids: Set<String> =
            if isSelected() {
                selection
            } else {
                [info.record.id]
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
