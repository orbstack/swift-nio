//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle
import Defaults

struct K8SSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject private var windowHolder = WindowHolder()

    @State private var k8sEnable = false

    @State private var presentConfirmResetK8sData = false

    var body: some View {
        SettingsStateWrapperView {
            Form {
                Toggle("Enable Kubernetes cluster", isOn: $k8sEnable)
                .onChange(of: k8sEnable) { newValue in
                    vmModel.trySetConfigKey(\.k8sEnable, newValue)
                }
                Text("Lightweight local cluster with UI & network integration. [Learn more](https://go.orbstack.dev/k8s)")
                .font(.subheadline)
                .foregroundColor(.secondary)

                Spacer()
                .frame(height: 32)

                HStack(spacing: 16) {
                    Button(action: {
                        Task {
                            // TODO fix this and add proper dirty check. this breaks dirty state of other configs
                            // needs to be set first, or k8s state wrapper doesn't update
                            vmModel.appliedConfig = vmModel.config

                            if let dockerRecord = vmModel.containers?.first(where: { $0.id == ContainerIds.docker }) {
                                await vmModel.tryRestartContainer(dockerRecord)
                            }
                        }
                    }) {
                        Text("Apply")
                    }
                    .disabled(vmModel.appliedConfig == vmModel.config)
                    .keyboardShortcut("s")

                    Button("Reset cluster", role: .destructive) {
                        presentConfirmResetK8sData = true
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
        }
        .padding()
        .background(WindowAccessor(holder: windowHolder))
        .alert("Reset Kubernetes cluster?", isPresented: $presentConfirmResetK8sData) {
            Button("Cancel", role: .cancel) {}
            Button("Reset", role: .destructive) {
                Task {
                    if let dockerRecord = vmModel.containers?.first(where: { $0.id == ContainerIds.docker }) {
                        await vmModel.tryInternalDeleteK8s()
                        await vmModel.tryStartContainer(dockerRecord)
                    }
                }
            }
        } message: {
            Text("All Kubernetes deployments, pods, services, and other data will be permanently lost.")
        }
    }

    private func updateFrom(_ config: VmConfig) {
        k8sEnable = config.k8sEnable
    }
}
