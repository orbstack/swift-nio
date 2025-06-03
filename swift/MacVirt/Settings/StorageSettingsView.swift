//
// Created by Danny Lin on 2/5/23.
//

import Combine
import Foundation
import LaunchAtLogin
import Sparkle
import SwiftUI

private enum DirItem: Hashable {
    case def
    case custom(String)
    case other
}

struct StorageSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject private var windowHolder = WindowHolder()

    @State private var mountHideShared = false
    @State private var dataDir: String?
    @State private var dataAllowBackup = false

    @State private var presentConfirmResetDockerData = false
    @State private var presentConfirmResetK8sData = false
    @State private var presentConfirmResetAllData = false

    var body: some View {
        SettingsStateWrapperView {
            Form {
                Section {
                    Toggle(
                        isOn: vmModel.bindingForConfig(\.mountHideShared, state: $mountHideShared)
                    ) {
                        Text("Hide OrbStack volume from Finder & Desktop")
                        Text(
                            "This volume makes it easy to access files in containers and machines.")
                    }
                } header: {
                    Text("Integration")
                }

                Section {
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
                            vmModel.trySetConfigKey(\.dataDir, nil)
                        case .custom:
                            // ignore
                            break
                        case .other:
                            selectFolder()
                        }
                    }
                    Picker(selection: selBinding, label: Text("Location")) {
                        Text("Default").tag(DirItem.def)
                        Divider()
                        if let dataDir {
                            Text(dataDir.split(separator: "/").last ?? "Custom")
                                .tag(DirItem.custom(dataDir))
                        }
                        Divider()
                        Text("Other…").tag(DirItem.other)
                    }

                    Toggle(
                        "Include data in Time Machine backups",
                        isOn: vmModel.bindingForConfig(\.dataAllowBackup, state: $dataAllowBackup))
                } header: {
                    Text("Data")
                }

                Section {
                    Button("Reset Docker Data", role: .destructive) {
                        presentConfirmResetDockerData = true
                    }

                    Button("Reset Kubernetes Cluster", role: .destructive) {
                        presentConfirmResetK8sData = true
                    }

                    Button("Reset All Data", role: .destructive) {
                        presentConfirmResetAllData = true
                    }
                } header: {
                    Text("Danger Zone")
                }

                SettingsFooter {
                    Button(action: {
                        Task {
                            await vmModel.tryRestart()
                        }
                    }) {
                        Text("Apply")
                        // TODO: dataAllowBackup doesn't require restart
                    }
                    .disabled(vmModel.appliedConfig == vmModel.config)
                    .keyboardShortcut("s")
                }
            }
            .formStyle(.grouped)
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
        .akAlert(
            "Reset Docker data?", isPresented: $presentConfirmResetDockerData,
            desc:
                "All containers, images, volumes, and Kubernetes resources will be permanently lost.",
            button1Label: "Reset",
            button1Action: {
                Task {
                    if let dockerMachine = vmModel.containers?.first(where: {
                        $0.id == ContainerIds.docker
                    }) {
                        await vmModel.tryDeleteContainer(dockerMachine.record)
                        await vmModel.tryStartContainer(dockerMachine.record)
                    }
                }
            },
            button2Label: "Cancel"
        )
        .akAlert(
            "Reset Kubernetes cluster?", isPresented: $presentConfirmResetK8sData,
            desc:
                "All Kubernetes deployments, pods, services, and other data will be permanently lost.",
            button1Label: "Reset",
            button1Action: {
                Task {
                    if let dockerMachine = vmModel.containers?.first(where: {
                        $0.id == ContainerIds.docker
                    }) {
                        await vmModel.tryInternalDeleteK8s()
                        await vmModel.tryStartContainer(dockerMachine.record)
                    }
                }
            },
            button2Label: "Cancel"
        )
        .akAlert(
            "Reset all data?", isPresented: $presentConfirmResetAllData,
            desc:
                "All containers, images, volumes, Kubernetes resources, and Linux machines will be permanently lost.",
            button1Label: "Reset",
            button1Action: {
                Task {
                    await vmModel.tryResetData()
                }
            },
            button2Label: "Cancel"
        )
        .windowHolder(windowHolder)
        .navigationTitle("Storage")
    }

    private func selectFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.canCreateDirectories = true
        panel.canDownloadUbiquitousContents = false
        panel.canResolveUbiquitousConflicts = false
        // initial directory
        panel.directoryURL = URL(fileURLWithPath: dataDir ?? Folders.userData)
        panel.message = "Select data location"

        let window = windowHolder.window ?? NSApp.keyWindow ?? NSApp.windows.first!
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
                let url = panel.url
            {
                if url.path == Folders.userData {
                    vmModel.trySetConfigKey(\.dataDir, nil)
                } else {
                    vmModel.trySetConfigKey(\.dataDir, url.path)
                }
            }
        }
    }

    private func updateFrom(_ config: VmConfig) {
        mountHideShared = config.mountHideShared
        dataDir = config.dataDir
        dataAllowBackup = config.dataAllowBackup
    }
}
