//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle

private enum DirItem: Hashable {
    case def
    case custom(String)
    case other
}

struct StorageSettingsView: BaseVmgrSettingsView, View {
    @EnvironmentObject internal var vmModel: VmViewModel

    @StateObject private var windowHolder = WindowHolder()

    @State private var mountHideShared = false
    @State private var dataDir: String?

    @State private var presentConfirmResetDockerData = false
    @State private var presentConfirmResetAllData = false

    var body: some View {
        StateWrapperView {
            Form {
                let selBinding = Binding<DirItem> {
                    if let dataDir {
                        return DirItem.custom(dataDir)
                    } else {
                        return DirItem.def
                    }
                } set: { newValue in
                    switch newValue {
                    case .def:
                        // update immediately to avoid picker glitch
                        dataDir = nil
                        setConfigKey(\.dataDir, nil)
                    case .custom:
                        // ignore
                        break
                    case .other:
                        selectFolder()
                    }
                }

                Toggle("Hide OrbStack volume (shared Docker & Linux files)", isOn: $mountHideShared)
                .onChange(of: mountHideShared) { newValue in
                    setConfigKey(\.mountHideShared, newValue)
                }
                VStack {
                    Picker(selection: selBinding, label: Text("Data location")) {
                        Text("Default").tag(DirItem.def)
                        Divider()
                        if let dataDir {
                            Text(dataDir.split(separator: "/").last ?? "Custom")
                            .tag(DirItem.custom(dataDir))
                        }
                        Divider()
                        Text("Other…").tag(DirItem.other)
                    }
                }
                .frame(maxWidth: 256)

                Spacer()
                .frame(height: 32)

                Button("Reset Docker data", role: .destructive) {
                    presentConfirmResetDockerData = true
                }

                Button("Reset all data", role: .destructive) {
                    presentConfirmResetAllData = true
                }

                Spacer()
                .frame(height: 32)

                Button(action: {
                    Task {
                        await vmModel.tryRestart()
                    }
                }) {
                    Text("Apply")
                    // TODO: dockerSetContext doesn't require restart
                }
                .disabled(vmModel.configAtLastStart == vmModel.config)
                .keyboardShortcut("s")
            }
            .onChange(of: vmModel.config) { config in
                if let config {
                    updateFrom(config)
                }
            }
            .onAppear {
                if let config = vmModel.config {
                    updateFrom(config)
                }
            }
        }
        .alert("Reset Docker data?", isPresented: $presentConfirmResetDockerData) {
            Button("Cancel", role: .cancel) {}
            Button("Reset", role: .destructive) {
                Task {
                    if let dockerRecord = vmModel.containers?.first(where: { $0.id == ContainerIds.docker }) {
                        await vmModel.tryDeleteContainer(dockerRecord)
                        await vmModel.tryStartContainer(dockerRecord)
                    }
                }
            }
        } message: {
            Text("All Docker containers, images, volumes, and other data will be permanently deleted.")
        }
        .alert("Reset all data?", isPresented: $presentConfirmResetAllData) {
            Button("Cancel", role: .cancel) {}
            Button("Reset", role: .destructive) {
                Task {
                    await vmModel.tryResetData()
                }
            }
        } message: {
            Text("All Docker data (containers, images, volumes, etc.) and Linux machines will be permanently deleted.")
        }
        .padding()
        .background(WindowAccessor(holder: windowHolder))
    }

    private func selectFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.canDownloadUbiquitousContents = false
        panel.canResolveUbiquitousConflicts = false
        // initial directory
        panel.directoryURL = URL(fileURLWithPath: dataDir ?? Folders.userData)

        let window = windowHolder.window ?? NSApp.keyWindow ?? NSApp.windows.first!
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
               let url = panel.url {
                if url.path == Folders.userData {
                    setConfigKey(\.dataDir, nil)
                } else {
                    setConfigKey(\.dataDir, url.path)
                }
            }
        }
    }

    private func updateFrom(_ config: VmConfig) {
        mountHideShared = config.mountHideShared
        dataDir = config.dataDir
    }
}
