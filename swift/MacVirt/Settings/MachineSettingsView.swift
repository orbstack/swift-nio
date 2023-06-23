//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle

protocol BaseVmgrSettingsView {
    var vmModel: VmViewModel { get }
}

extension BaseVmgrSettingsView {
    func setConfigKey<T: Equatable>(_ keyPath: WritableKeyPath<VmConfig, T>, _ newValue: T) {
        Task { @MainActor in
            if var config = vmModel.config {
                config[keyPath: keyPath] = newValue
                await vmModel.trySetConfig(config)
            }
        }
    }
}

private enum DirItem: Hashable {
    case def
    case custom(String)
    case other
}

struct MachineSettingsView: BaseVmgrSettingsView, View {
    @EnvironmentObject internal var vmModel: VmViewModel

    @StateObject private var windowHolder = WindowHolder()

    @State private var memoryMib = 0.0
    @State private var cpu = 1.0
    @State private var enableRosetta = true
    @State private var mountHideShared = false
    @State private var dataDir: String?

    var body: some View {
        Form {
            Group {
                switch vmModel.state {
                case .stopped:
                    VStack {
                        Text("Service must be running to change settings.")
                        Button(action: {
                            Task {
                                await vmModel.tryStartAndWait()
                            }
                        }) {
                            Text("Start")
                        }
                    }

                case .running:
                    #if arch(arm64)
                    if #available(macOS 13, *) {
                        Toggle("Use Rosetta to run Intel code", isOn: $enableRosetta)
                            .onChange(of: enableRosetta) { newValue in
                                setConfigKey(\.rosetta, newValue)
                            }
                    } else {
                        Toggle("Use Rosetta to run Intel code", isOn: .constant(false))
                            .disabled(true)
                        Text("Requires macOS 13 or newer")
                                .font(.subheadline)
                                .foregroundColor(.secondary)
                    }
                    #endif

                    Toggle("Hide OrbStack volume (shared Docker & Linux files)", isOn: $mountHideShared)
                            .onChange(of: mountHideShared) { newValue in
                                setConfigKey(\.mountHideShared, newValue)
                            }

                    Spacer()
                            .frame(height: 32)

                    let maxMemoryMib = Double(ProcessInfo.processInfo.physicalMemory) * 0.75 / 1024.0 / 1024.0
                    Slider(value: $memoryMib, in: 1024...maxMemoryMib, step: 1024) {
                        VStack(alignment: .trailing) {
                            Text("Memory limit")
                            Text("\(memoryMib / 1024, specifier: "%.0f") GiB")
                                    .font(.caption.monospacedDigit())
                                    .foregroundColor(.secondary)
                        }
                    } minimumValueLabel: {
                        Text("1 GiB")
                    } maximumValueLabel: {
                        Text("\(maxMemoryMib / 1024, specifier: "%.0f") GiB")
                    }
                    .onChange(of: memoryMib) { newValue in
                        setConfigKey(\.memoryMib, UInt64(newValue))
                    }

                    let maxCpu = ProcessInfo.processInfo.processorCount
                    Slider(value: $cpu, in: 1...Double(maxCpu), step: 1) {
                        VStack(alignment: .trailing) {
                            Text("CPU limit")
                            let intCpu = Int(cpu + 0.5)
                            let label = (intCpu == maxCpu) ? "None" : "\(intCpu)00%"
                            Text(label)
                                    .font(.caption.monospacedDigit())
                                    .foregroundColor(.secondary)
                        }
                    } minimumValueLabel: {
                        Text("100%")
                    } maximumValueLabel: {
                        Text("None")
                    }
                    .onChange(of: cpu) { newValue in
                        setConfigKey(\.cpu, UInt(newValue))
                    }
                    Text("Resources are used on demand, up to these limits.")
                            .font(.subheadline)
                            .foregroundColor(.secondary)

                    Spacer()
                        .frame(height: 32)

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

                    Spacer()
                        .frame(height: 32)

                    Button(action: {
                        Task {
                            await vmModel.tryRestart()
                        }
                    }) {
                        Text("Apply")
                    }.disabled(vmModel.configAtLastStart == vmModel.config)

                default:
                    ProgressView()
                }
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
        memoryMib = Double(config.memoryMib)
        cpu = Double(config.cpu)
        enableRosetta = config.rosetta
        mountHideShared = config.mountHideShared
        dataDir = config.dataDir
    }
}
