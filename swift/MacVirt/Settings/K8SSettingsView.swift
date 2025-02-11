//
// Created by Danny Lin on 2/5/23.
//

import Combine
import Defaults
import Foundation
import LaunchAtLogin
import Sparkle
import SwiftUI

struct K8SSettingsView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject private var windowHolder = WindowHolder()

    @State private var k8sEnable = false
    @State private var k8sExposeServices = false

    @State private var presentConfirmResetK8sData = false

    var body: some View {
        SettingsStateWrapperView {
            Form {
                Toggle(
                    "Enable Kubernetes cluster",
                    isOn: vmModel.bindingForConfig(\.k8sEnable, state: $k8sEnable))
                Text(
                    "Lightweight local cluster with UI & network integration. [Learn more](https://go.orbstack.dev/k8s)"
                )
                .font(.subheadline)
                .foregroundColor(.secondary)

                Spacer()
                    .frame(height: 32)

                Toggle(
                    "Expose services to local network devices",
                    isOn: vmModel.bindingForConfig(\.k8sExposeServices, state: $k8sExposeServices))
                Text("Includes NodePorts, LoadBalancers, and the Kubernetes API.")
                    .font(.subheadline)
                    .foregroundColor(.secondary)

                Spacer()
                    .frame(height: 32)

                HStack(spacing: 16) {
                    Button(action: {
                        Task {
                            // restart only
                            await vmModel.tryStartStopK8s(enable: k8sEnable, force: true)
                        }
                    }) {
                        Text("Apply")
                    }
                    .disabled(vmModel.appliedConfig == vmModel.config)
                    .keyboardShortcut("s")

                    Button("Reset Cluster", role: .destructive) {
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
        .akAlert(
            "Reset Kubernetes cluster?", isPresented: $presentConfirmResetK8sData,
            desc:
                "All Kubernetes deployments, pods, services, and other data will be permanently lost.",
            button1Label: "Reset",
            button1Action: {
                Task {
                    if let dockerMachine = vmModel.containers?.first(where: {
                        $0.id == ContainerIds.docker
                    }) {
                        await vmModel.tryInternalDeleteK8s()
                        await vmModel.tryStartContainer(dockerMachine.record)
                    }
                }
            },
            button2Label: "Cancel")
    }

    private func updateFrom(_ config: VmConfig) {
        k8sEnable = config.k8sEnable
        k8sExposeServices = config.k8sExposeServices
    }
}
