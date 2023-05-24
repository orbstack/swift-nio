//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle

struct NetworkSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @Environment(\.controlActiveState) private var controlActiveState: ControlActiveState
    @State private var proxyText = ""
    @State private var proxyMode = "auto"

    var body: some View {
        Form {
            Group {
                switch vmModel.state {
                case .stopped:
                    VStack {
                        Text("Machine must be running to change settings.")
                        Button(action: {
                            Task {
                                await vmModel.tryStartAndWait()
                            }
                        }) {
                            Text("Start")
                        }
                    }

                case .running:
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
        var configValue: String
        switch proxyMode {
        case "auto":
            configValue = "auto"
        case "none":
            configValue = "none"
        default:
            configValue = proxyText == "" ? "auto" : proxyText
        }

        Task {
            if let config = vmModel.config,
               config.networkProxy != configValue {
                await vmModel.tryPatchConfig(VmConfigPatch(networkProxy: configValue))
            }
        }
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
    }
}
