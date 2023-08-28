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
    let onRefresh: () async -> Void

    init(_ keyPath: KeyPath<VmViewModel, [Entity]?>,
         @ViewBuilder content: @escaping ([Entity], ContainerRecord) -> Content,
         onRefresh: @escaping () async -> Void) {
        self.keyPath = keyPath
        self.content = content
        self.onRefresh = onRefresh
    }

    private var disabledView: some View {
        VStack(spacing: 16) { // match ContentUnavailableViewCompat desc padding
            ContentUnavailableViewCompat("Kubernetes Disabled", systemImage: "helm")

            Button(action: {
                Task {
                    await vmModel.trySetConfigKeyAsync(\.k8sEnable, true)
                    // TODO fix this and add proper dirty check. this breaks dirty state of other configs
                    // needs to be set first, or k8s state wrapper doesn't update
                    vmModel.appliedConfig = vmModel.config

                    if let dockerRecord = vmModel.containers?.first(where: { $0.id == ContainerIds.docker }) {
                        await vmModel.tryRestartContainer(dockerRecord)
                    }
                    await onRefresh()
                }
            }) {
                Text("Turn on")
                .padding(.horizontal, 4)
            }
            .buttonStyle(.borderedProminent)
            .keyboardShortcut(.defaultAction)
            .controlSize(.large)
        }
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
                           dockerRecord.state != .stopped {
                            content(entities, dockerRecord)
                        } else if dockerRecord.state == .stopped || !config.k8sEnable {
                            disabledView
                        } else {
                            ProgressView(label: {
                                Text("Loading")
                            })
                        }
                    }
                    .onChange(of: dockerRecord.state) { _ in
                        Task {
                            await onRefresh()
                        }
                    }
                } else {
                    ProgressView(label: {
                        Text("Loading")
                    })
                }
            }
            .task {
                NSLog("refresh: k8s task")
                await onRefresh()
            }
            .onChange(of: controlActiveState) { state in
                if state == .key {
                    Task {
                        await onRefresh()
                    }
                }
            }
        }
    }
}