//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle

struct NetworkSettingsView: BaseVmgrSettingsView, View {
    @EnvironmentObject internal var vmModel: VmViewModel
    @Environment(\.controlActiveState) private var controlActiveState: ControlActiveState
    @State private var proxyText = ""
    @State private var proxyMode = "auto"
    @State private var networkBridge = true

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
                    Toggle("Allow access to container domains & IPs", isOn: $networkBridge)
                        .onChange(of: networkBridge) { newValue in
                            setConfigKey(\.networkBridge, newValue)

                            // restart Docker if running
                            if newValue != vmModel.config?.networkBridge {
                                if vmModel.state == .running,
                                   let machines = vmModel.containers,
                                   let dockerRecord = machines.first(where: { $0.id == ContainerIds.docker }),
                                   dockerRecord.state == .starting || dockerRecord.state == .running {
                                    Task { @MainActor in
                                        await vmModel.tryRestartContainer(dockerRecord)
                                    }
                                }
                            }
                        }
                    Text("Use domains and IPs to connect to containers without port forwarding.")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                    Text("This also includes Linux machines. [Learn more](https://docs.orbstack.dev/readme-link/container-domains)")
                        .font(.subheadline)
                        .foregroundColor(.secondary)

                    Spacer().frame(height: 32)

                    Picker("Proxy", selection: $proxyMode) {
                        Text("Automatic (system)").tag("auto")
                        Text("Custom").tag("custom")
                        Text("None").tag("none")
                    }.pickerStyle(.radioGroup)

                    Spacer().frame(height: 20)

                    //TODO validate url on our side
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

        setConfigKey(\.networkProxy, proxyValue)
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
    }
}
