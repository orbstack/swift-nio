//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle

struct DockerSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @State private var enableIPv6 = false
    @State private var configJson = "{\n}"

    var body: some View {
        Form {
            Group {
                Toggle(isOn: $enableIPv6, label: {
                    Text("Enable IPv6")
                })
            }

            Spacer()
                .frame(height: 32)

            Group {
                Text("Advanced engine config")
                        .font(.headline)
                        .padding(.bottom, 4)

                TextEditor(text: $configJson)
                        .font(.body.monospaced())
                        .frame(minHeight: 150)
                        .autocorrectionDisabled()

                Text("You can also [edit the config file](https://docs.orbstack.dev/docker/#engine-config) directly.\nInvalid configs will prevent Docker from starting.")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .padding(.top, 4)
                        .padding(.bottom, 8)

                if vmModel.state == .running,
                   let machines = vmModel.containers,
                   let dockerRecord = machines.first(where: { $0.id == ContainerIds.docker }) {
                    Button("Apply") {
                        Task.detached {
                            let saved = await save()
                            if saved {
                                await vmModel.tryRestartContainer(dockerRecord)
                            }
                        }
                    }
                    .disabled(!hasChanges())
                    .keyboardShortcut("s")
                }
            }
        }
        .padding()
        .navigationTitle("Settings")
        .onAppear {
            // not MainActor: blocking sync I/O
            Task.detached {
                await vmModel.tryLoadDockerConfig()
            }
        }
        .onDisappear {
            // not MainActor: blocking sync I/O
            Task.detached {
                await save()
            }
        }
        .onChange(of: vmModel.dockerConfigJson) { newValue in
            configJson = newValue
        }
        .onChange(of: vmModel.dockerEnableIPv6) { newValue in
            enableIPv6 = newValue
        }
    }

    private func save() async -> Bool {
        guard hasChanges() else {
            return false
        }

        await vmModel.trySetDockerConfig(configJson: configJson, enableIpv6: enableIPv6)
        return true
    }

    private func hasChanges() -> Bool {
        return configJson != vmModel.dockerConfigJson || enableIPv6 != vmModel.dockerEnableIPv6
    }
}

// HACK to work-around the smart quote issue
extension NSTextView {
    open override var frame: CGRect {
        didSet {
            self.isAutomaticQuoteSubstitutionEnabled = false
        }
    }
}