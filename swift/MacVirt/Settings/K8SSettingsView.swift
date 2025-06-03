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
            SettingsForm {
                Section {
                    Toggle(isOn: vmModel.bindingForConfig(\.k8sEnable, state: $k8sEnable)) {
                        Text("Enable Kubernetes cluster")
                        Text(
                            "Lightweight local cluster with UI & network integration. [Learn more](https://orb.cx/k8s)"
                        )
                    }
                }

                Section {
                    Toggle(
                        isOn: vmModel.bindingForConfig(
                            \.k8sExposeServices, state: $k8sExposeServices)
                    ) {
                        Text("Expose services to local network devices")
                        Text("Includes NodePorts, LoadBalancers, and the Kubernetes API.")
                    }
                } header: {
                    Text("Network")
                }

                SettingsFooter {
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
        .navigationTitle("Kubernetes")
        .windowHolder(windowHolder)
        .akAlert(isPresented: $presentConfirmResetK8sData, style: .critical) {
            "Reset Kubernetes cluster?"
            "All Kubernetes deployments, pods, services, and other data will be permanently lost."

            AKAlertButton("Reset", destructive: true) {
                Task {
                    if let dockerMachine = vmModel.containers?.first(where: {
                        $0.id == ContainerIds.docker
                    }) {
                        await vmModel.tryInternalDeleteK8s()
                        await vmModel.tryStartContainer(dockerMachine.record)
                    }
                }
            }
            AKAlertButton("Cancel")
        }
    }

    private func updateFrom(_ config: VmConfig) {
        k8sEnable = config.k8sEnable
        k8sExposeServices = config.k8sExposeServices
    }
}
