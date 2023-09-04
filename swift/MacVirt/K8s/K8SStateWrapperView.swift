//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct K8SStateWrapperView<Content: View, Entity: K8SResource>: View {
    @Environment(\.controlActiveState) private var controlActiveState: ControlActiveState
    @EnvironmentObject private var vmModel: VmViewModel

    let keyPath: KeyPath<VmViewModel, [Entity]?>
    let content: ([Entity], ContainerRecord) -> Content

    init(_ keyPath: KeyPath<VmViewModel, [Entity]?>,
         @ViewBuilder content: @escaping ([Entity], ContainerRecord) -> Content) {
        self.keyPath = keyPath
        self.content = content
    }

    private var disabledView: some View {
        VStack(spacing: 16) { // match ContentUnavailableViewCompat desc padding
            ContentUnavailableViewCompat("Kubernetes Disabled", systemImage: "helm")

            Button(action: {
                Task {
                    await vmModel.tryStartStopK8s(enable: true)
                }
            }) {
                Text("Turn On")
                .padding(.horizontal, 4)
            }
            .buttonStyle(.borderedProminent)
            .keyboardShortcut(.defaultAction)
            .controlSize(.large)
        }
    }

    private var isK8sClusterCreating: Bool {
        // if there are no kube-system pods
        // api server resource is always there
        if let pods = vmModel.k8sPods {
            return !pods.contains(where: { $0.namespace == "kube-system" })
        }
        return false
    }

    var body: some View {
        StateWrapperView {
            Group {
                // TODO return verdict as enum and use switch{} to fix loading flicker
                if let machines = vmModel.containers,
                   let dockerRecord = machines.first(where: { $0.id == ContainerIds.docker }),
                   let config = vmModel.appliedConfig { // applied config, not current
                    Group {
                        if let entities = vmModel[keyPath: keyPath],
                           dockerRecord.state != .stopped,
                           !isK8sClusterCreating {
                            content(entities, dockerRecord)
                        } else if dockerRecord.state == .stopped || !config.k8sEnable {
                            disabledView
                        } else if isK8sClusterCreating {
                            ProgressView(label: {
                                Text("Creating cluster")
                            })
                        } else {
                            ProgressView(label: {
                                Text("Loading")
                            })
                        }
                    }
                } else {
                    ProgressView(label: {
                        Text("Loading")
                    })
                }
            }
        }
    }
}