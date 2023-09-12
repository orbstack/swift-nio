//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle
import Defaults

struct MachineSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject private var windowHolder = WindowHolder()

    @State private var memoryMib = 0.0
    @State private var cpu = 1.0
    @State private var enableRosetta = true
    @State private var dockerSetContext = true
    @State private var setupUseAdmin = true

    @State private var presentDisableAdmin = false

    var body: some View {
        SettingsStateWrapperView {
            Form {
                #if arch(arm64)
                Group {
                    if #available(macOS 13, *) {
                        Toggle("Use Rosetta to run Intel code", isOn: $enableRosetta)
                        .onChange(of: enableRosetta) { newValue in
                            vmModel.trySetConfigKey(\.rosetta, newValue)
                        }
                        Text("Faster. Only disable if you run into compatibility issues.")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                    } else {
                        Toggle("Use Rosetta to run Intel code", isOn: .constant(false))
                        .disabled(true)
                        Text("Requires macOS 13 or newer")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                    }

                    Spacer()
                    .frame(height: 32)
                }
                #endif

                Group {
                    let systemMemMib = Double(ProcessInfo.processInfo.physicalMemory / 1024 / 1024)
                    // 80% or (max - 4 GiB), whichever is greater
                    // e.g. up to 28 GiB on 32 GiB Macs
                    // OK because of macOS compression
                    let maxMemoryMib = max(systemMemMib * 0.80, systemMemMib - 4096)
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
                        vmModel.trySetConfigKey(\.memoryMib, UInt64(newValue))
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
                        vmModel.trySetConfigKey(\.cpu, UInt(newValue))
                    }
                    Text("Resources are used on demand, up to these limits. [Learn more](https://go.orbstack.dev/res-limits)")
                    .font(.subheadline)
                    .foregroundColor(.secondary)
                }

                Spacer()
                .frame(height: 32)

                Toggle("Switch Docker & Kubernetes context automatically", isOn: $dockerSetContext)
                .onChange(of: dockerSetContext) { newValue in
                    vmModel.trySetConfigKey(\.dockerSetContext, newValue)
                }

                let adminBinding = Binding<Bool>(
                        get: { Users.hasAdmin && setupUseAdmin },
                        set: { newValue in
                            if newValue {
                                vmModel.trySetConfigKey(\.setupUseAdmin, true)
                                // reset dismiss count
                                Defaults[.adminDismissCount] = 0
                            } else {
                                presentDisableAdmin = true
                            }
                        }
                )
                Toggle("Use admin privileges for enhanced features", isOn: adminBinding)
                .disabled(!Users.hasAdmin) // disabled + false if no admin
                Text("This can improve performance and compatibility. [Learn more](https://go.orbstack.dev/admin)")
                .font(.subheadline)
                .foregroundColor(.secondary)

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
                .disabled(vmModel.appliedConfig == vmModel.config)
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
        .padding()
        .background(WindowAccessor(holder: windowHolder))
        .alert("Disable privileged features?", isPresented: $presentDisableAdmin) {
            Button("Cancel", role: .cancel) {}
            Button("Disable", role: .destructive) {
                vmModel.trySetConfigKey(\.setupUseAdmin, false)
            }
        } message: {
            Text("""
                 This will disable performance improvements, better Docker compatibility, and potentially more features in the future.

                 We recommend keeping this on.
                 """)
        }
    }

    private func updateFrom(_ config: VmConfig) {
        // fix slider oscillating
        if memoryMib == 0.0 {
            memoryMib = Double(config.memoryMib)
        }
        cpu = Double(config.cpu)
        enableRosetta = config.rosetta
        dockerSetContext = config.dockerSetContext
        setupUseAdmin = config.setupUseAdmin
    }
}
