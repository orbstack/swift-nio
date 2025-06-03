//
// Created by Danny Lin on 2/5/23.
//

import Combine
import Foundation
import LaunchAtLogin
import Sparkle
import SwiftUI

struct DockerSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @State private var enableIPv6 = false
    @State private var configJson = "{\n}"

    var body: some View {
        SettingsForm {
            Section {
                Toggle(isOn: $enableIPv6) {
                    Text("Enable IPv6")
                    Text("May cause compatibility issues with some containers.")
                }
            } header: {
                Text("Network")
            }

            Section {
                TextEditor(text: $configJson)
                    .font(.body.monospaced())
                    .frame(minHeight: 150)
                    .autocorrectionDisabled()
            } header: {
                Text("Advanced engine configuration")
                Text(
                    "You can also [edit the config file](https://orb.cx/docker-config) directly. Invalid configs will prevent Docker from starting."
                )
            }

            SettingsFooter {
                if vmModel.state == .running,
                    let dockerMachine = vmModel.dockerMachine
                {
                    Button("Apply") {
                        Task.detached {
                            let saved = await save()
                            if saved {
                                await vmModel.tryRestartContainer(dockerMachine.record)
                            }
                        }
                    }
                    .disabled(!hasChanges())
                    .keyboardShortcut("s")
                }
            }
        }
        .navigationTitle("Docker")
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
    override open var frame: CGRect {
        didSet {
            isAutomaticQuoteSubstitutionEnabled = false
        }
    }
}
