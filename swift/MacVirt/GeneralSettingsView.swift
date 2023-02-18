//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle

struct GeneralSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @State private var memoryMib = 0.0
    @State private var enableRosetta = true

    @AppStorage("onboardingCompleted") private var onboardingCompleted = false

    let updaterController: SPUStandardUpdaterController

    var body: some View {
        Form {
            LaunchAtLogin.Toggle {
                Text("Start at login")
            }

            UpdaterSettingsView(updater: updaterController.updater)

            Group {
                if vmModel.state == .running {
                    Toggle("Use Rosetta to run x86 code", isOn: $enableRosetta)
                    .onChange(of: enableRosetta) { newValue in
                        Task {
                            if var config = vmModel.config {
                                config.enableRosetta = newValue
                                await vmModel.tryPatchConfig(config)
                            }
                        }
                    }

                    let maxMemoryMib = Double(ProcessInfo.processInfo.physicalMemory) * 0.75 / 1024.0 / 1024.0
                    Slider(value: $memoryMib, in: 1024...maxMemoryMib, step: 1024) {
                        VStack {
                            Text("Memory")
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
                        Task {
                            if var config = vmModel.config {
                                config.memoryMib = UInt64(newValue)
                                await vmModel.tryPatchConfig(config)
                            }
                        }
                    }
                    Text("Takes effect after VM restart.")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                } else {
                    ProgressView()
                }
            }
            .onChange(of: vmModel.config) { config in
                if let config {
                    memoryMib = Double(config.memoryMib)
                    enableRosetta = config.enableRosetta
                }
            }
            .onAppear {
                if let config = vmModel.config {
                    memoryMib = Double(config.memoryMib)
                }
            }

            #if DEBUG
            Button(action: {
                onboardingCompleted = false
            }) {
                Text("reset onboarding")
            }
            #endif
        }
        .padding()
        .navigationTitle("Settings")
    }
}
