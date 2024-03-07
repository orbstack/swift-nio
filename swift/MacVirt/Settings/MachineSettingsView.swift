//
// Created by Danny Lin on 2/5/23.
//

import Combine
import Defaults
import Foundation
import LaunchAtLogin
import Sparkle
import SwiftUI

struct MachineSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject private var windowHolder = WindowHolder()

    @State private var memoryMib: UInt64 = 0
    @State private var cpu: UInt = 1
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
                            Toggle("Use Rosetta to run Intel code",
                                   isOn: vmModel.bindingForConfig(\.rosetta, state: $enableRosetta))

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
                    let systemMemMib = ProcessInfo.processInfo.physicalMemory / 1024 / 1024
                    // 80% or (max - 4 GiB), whichever is greater
                    // e.g. up to 28 GiB on 32 GiB Macs
                    // OK because of macOS compression
                    let maxMemoryMib = max(systemMemMib * 80 / 100, systemMemMib - 4096)

                    let memoryMibBinding = vmModel.bindingForConfig(\.memoryMib, state: $memoryMib)
                    Slider(value: memoryMibBinding, in: 1024 ... maxMemoryMib, step: 1024) {
                        VStack(alignment: .trailing) {
                            Text("Memory limit")
                            Text("\(memoryMibBinding.wrappedValue / 1024) GiB")
                                .font(.caption.monospacedDigit())
                                .foregroundColor(.secondary)
                        }
                    } minimumValueLabel: {
                        Text("1 GiB")
                    } maximumValueLabel: {
                        Text("\(maxMemoryMib / 1024) GiB")
                    }

                    let maxCpu = UInt(ProcessInfo.processInfo.processorCount)

                    let cpuBinding = vmModel.bindingForConfig(\.cpu, state: $cpu)
                    Slider(value: cpuBinding, in: 1 ... maxCpu, step: 1) {
                        VStack(alignment: .trailing) {
                            Text("CPU limit")
                            let curCpu = cpuBinding.wrappedValue
                            let label = (curCpu == maxCpu) ? "None" : "\(curCpu)00%"
                            Text(label)
                                .font(.caption.monospacedDigit())
                                .foregroundColor(.secondary)
                        }
                    } minimumValueLabel: {
                        Text("100%")
                    } maximumValueLabel: {
                        Text("None")
                    }

                    Text("Resources are used on demand, up to these limits. [Learn more](https://go.orbstack.dev/res-limits)")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                }

                Spacer()
                    .frame(height: 32)

                Toggle("Switch Docker & Kubernetes context automatically",
                       isOn: vmModel.bindingForConfig(\.dockerSetContext, state: $dockerSetContext))

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
        .akAlert("Disable privileged features?", isPresented: $presentDisableAdmin,
                desc: """
                      This will disable performance improvements, better Docker compatibility, and potentially more features in the future.

                      We recommend keeping this on.
                      """,
                button1Label: "Disable",
                button1Action: { vmModel.trySetConfigKey(\.setupUseAdmin, false) },
                button2Label: "Cancel")
    }

    private func updateFrom(_ config: VmConfig) {
        memoryMib = config.memoryMib
        cpu = config.cpu
        enableRosetta = config.rosetta
        dockerSetContext = config.dockerSetContext
        setupUseAdmin = config.setupUseAdmin
    }
}
