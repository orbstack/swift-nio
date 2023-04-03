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
    @State private var networkProxy = ""

    var body: some View {
        Form {
            Group {
                switch vmModel.state {
                case .stopped:
                    VStack {
                        Text("Machine must be running to change settings.")
                        Button(action: {
                            Task {
                                await vmModel.start()
                            }
                        }) {
                            Text("Start")
                        }
                    }

                case .running:
                    TextField("Proxy", text: $networkProxy)
                            .onSubmit {
                                commit()
                            }
                    Text("HTTP, HTTPS, or SOCKS proxy for all Docker and Linux traffic.")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                    Text("System proxy settings are used if not set.")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                    // suppress markdown
                    Text(String("Example: socks5://user:pass@example.com:1080"))
                            .font(.subheadline)
                            .foregroundColor(.secondary)

                default:
                    ProgressView()
                }
            }
            .onChange(of: vmModel.config) { config in
                if let config {
                    networkProxy = config.networkProxy
                }
            }
            .onAppear {
                if let config = vmModel.config {
                    networkProxy = config.networkProxy
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
        .navigationTitle("Settings")
    }

    func commit() {
        Task {
            if let config = vmModel.config,
               config.networkProxy != networkProxy {
                await vmModel.tryPatchConfig(VmConfigPatch(networkProxy: networkProxy))
            }
        }
    }
}
