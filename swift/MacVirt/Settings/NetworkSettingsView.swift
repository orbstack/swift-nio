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
            Form {
                Toggle("Allow access to container domains & IPs", isOn: $networkBridge)
                    .onChange(of: networkBridge) { newValue in
                        vmModel.trySetConfigKey(\.networkBridge, newValue)

                        // restart Docker if running
                        if newValue != vmModel.config?.networkBridge {
                            if vmModel.state == .running,
                               let machines = vmModel.containers,
                               let dockerRecord = machines.first(where: { $0.id == ContainerIds.docker }),
                               dockerRecord.state == .starting || dockerRecord.state == .running
                            {
                                Task { @MainActor in
                                    await vmModel.tryRestartContainer(dockerRecord)
                                }
                            }
                        }
                    }
                Text("Use domains and IPs to connect to containers without port forwarding.")
                    .font(.subheadline)
                    .foregroundColor(.secondary)
                Text("This also includes Linux machines. [Learn more](https://go.orbstack.dev/container-domains)")
                    .font(.subheadline)
                    .foregroundColor(.secondary)

                Toggle("Enable HTTPS for container domains", isOn: $networkHttps)
                    .onChange(of: networkHttps) { newValue in
                        // this one is live-reload
                        vmModel.trySetConfigKey(\.networkHttps, newValue)
                    }
                    .disabled(!networkBridge)

                Spacer().frame(height: 32)

                Picker("Proxy", selection: $proxyMode) {
                    Text("Automatic (system)").tag("auto")
                    Text("Custom").tag("custom")
                    Text("None").tag("none")
                }
                .pickerStyle(.radioGroup)

                Spacer().frame(height: 20)

                // TODO: validate url on our side
                TextField("", text: $proxyText)
                    .onSubmit {
                        commit()
                    }
                    .disabled(proxyMode != "custom")

                Text("HTTP, HTTPS, or SOCKS proxy for all Docker and Linux traffic.")
                    .font(.subheadline)
                    .foregroundColor(.secondary)
                // suppress markdown
                Text(String(proxyMode == "custom" ? "Example: socks5://user:pass@example.com:1080" : ""))
                    .font(.subheadline)
                    .foregroundColor(.secondary)
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
        .padding()
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
