//
// Created by Danny Lin on 2/5/23.
//

import Combine
import Foundation
import LaunchAtLogin
import Sparkle
import SwiftUI

struct NetworkSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @Environment(\.controlActiveState) private var controlActiveState: ControlActiveState
    @State private var proxyText = ""
    @State private var proxyMode = "auto"
    @State private var networkBridge = true
    @State private var networkHttps = false

    var body: some View {
        SettingsStateWrapperView {
            SettingsForm {
                Section {
                    let networkBridgeBinding = Binding {
                        networkBridge
                    } set: { newValue in
                        vmModel.trySetConfigKey(\.networkBridge, newValue)

                        // restart Docker if running
                        if newValue != vmModel.config?.networkBridge,
                            vmModel.state == .running,
                            let dockerMachine = vmModel.dockerMachine,
                            dockerMachine.record.state == .starting
                                || dockerMachine.record.state == .running
                        {
                            Task { @MainActor in
                                await vmModel.tryRestartContainer(dockerMachine.record)
                            }
                        }
                    }
                    Toggle(isOn: networkBridgeBinding) {
                        Text("Allow access to container domains & IPs")
                        Text(
                            "Use domains and IPs to connect to containers and machines without port forwarding. [Learn more](https://orb.cx/container-domains)"
                        )
                    }

                    // this one is live-reload
                    Toggle(
                        "Enable HTTPS for container domains",
                        isOn: vmModel.bindingForConfig(\.networkHttps, state: $networkHttps)
                    )
                    .disabled(!networkBridge)
                }

                Section {
                    Picker(selection: $proxyMode) {
                        Text("Auto (system)").tag("auto")
                        Text("Custom").tag("custom")
                        Text("None").tag("none")
                    } label: {
                        Text("Proxy")
                        Text(
                            "Apply an HTTP, HTTPS, or SOCKS proxy to all traffic from containers and machines."
                        )
                    }
                    .pickerStyle(.radioGroup)

                    if proxyMode == "custom" {
                        // TODO: validate url on our side
                        TextField(
                            "URL", text: $proxyText,
                            prompt: Text("socks5://user:pass@example.com:1080")
                        )
                        .onSubmit {
                            commit()
                        }
                    }
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
            .onDisappear {
                commit()
            }
            .onChange(of: controlActiveState) { state in
                if state != .key {
                    commit()
                }
            }
        }
        .akNavigationTitle("Network")
    }

    private func commit() {
        var proxyValue: String
        switch proxyMode {
        case "auto":
            proxyValue = "auto"
        case "none":
            proxyValue = "none"
        default:
            proxyValue = proxyText == "" ? "auto" : proxyText
        }

        vmModel.trySetConfigKey(\.networkProxy, proxyValue)
    }

    private func updateFrom(_ config: VmConfig) {
        switch config.networkProxy {
        case "auto":
            proxyMode = "auto"
            proxyText = ""
        case "none":
            proxyMode = "none"
            proxyText = ""
        default:
            proxyMode = "custom"
            proxyText = config.networkProxy
        }

        networkBridge = config.networkBridge
        networkHttps = config.networkHttps
    }
}
