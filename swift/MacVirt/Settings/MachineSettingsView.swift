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

    @State private var memory = 0.0
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
                            let enableRosettaBinding = bindingWithAction($enableRosetta) { newValue in
                                vmModel.trySetConfigKey(\.rosetta, newValue)
                            }

                            Toggle("Use Rosetta to run Intel code", isOn: enableRosettaBinding)

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

                    let memoryMibBinding = bindingWithAction($memory) { newValue in
                        vmModel.memoryMib = newValue
                    }

                    Slider(value: memoryMibBinding, in: 1024 ... maxMemoryMib, step: 1024) {
                        VStack(alignment: .trailing) {
                            Text("Memory limit")
                            Text("\(memoryMibBinding.wrappedValue / 1024, specifier: "%.0f") GiB")
                                .font(.caption.monospacedDigit())
                                .foregroundColor(.secondary)
                        }
                    } minimumValueLabel: {
                        Text("1 GiB")
                    } maximumValueLabel: {
                        Text("\(memoryMibBinding.wrappedValue / 1024, specifier: "%.0f") GiB")
                    }

                    let maxCpu = ProcessInfo.processInfo.processorCount

                    // add intermediate Binding that only calls `vmModel.trySetConfigKey` when the user manually drags the slider
                    let cpuBinding = bindingWithAction($cpu) { newValue in
                        vmModel.trySetConfigKey(\.cpu, UInt(newValue))
                    }

                    Slider(value: cpuBinding, in: 1 ... Double(maxCpu), step: 1) {
                        VStack(alignment: .trailing) {
                            Text("CPU limit")
                            let intCpu = Int(cpuBinding.wrappedValue + 0.5)
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

                    Text("Resources are used on demand, up to these limits. [Learn more](https://go.orbstack.dev/res-limits)")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                }

                Spacer()
                    .frame(height: 32)

                let dockerSetContextBinding = bindingWithAction($dockerSetContext) { newValue in
                    vmModel.trySetConfigKey(\.dockerSetContext, newValue)
                }
                Toggle("Switch Docker & Kubernetes context automatically", isOn: dockerSetContextBinding)

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
        vmModel.memoryMib = Double(config.memoryMib)
        memory = Double(config.memoryMib)
        cpu = Double(config.cpu)
        enableRosetta = config.rosetta
        dockerSetContext = config.dockerSetContext
        setupUseAdmin = config.setupUseAdmin
    }

    func bindingWithAction<T>(_ binding: Binding<T>, action: @escaping (_ newValue: T) -> Void) -> Binding<T> {
        return Binding<T> {
            binding.wrappedValue
        } set: { newValue in
            binding.wrappedValue = newValue
            action(newValue)
        }
    }
}
