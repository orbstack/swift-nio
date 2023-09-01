//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerStateWrapperView<Content: View, Entity: Codable>: View {
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

    var body: some View {
        // TODO return verdict as enum and use switch{} to fix loading flicker
        StateWrapperView {
            Group {
                if let machines = vmModel.containers,
                   let dockerRecord = machines.first(where: { $0.id == ContainerIds.docker }) {
                    Group {
                        if let entities = vmModel[keyPath: keyPath],
                           dockerRecord.state != .stopped {
                            content(entities, dockerRecord)
                        } else if dockerRecord.state == .stopped {
                            VStack(spacing: 16) { // match ContentUnavailableViewCompat desc padding
                                ContentUnavailableViewCompat("Docker Disabled", systemImage: "shippingbox.fill")

                                Button(action: {
                                    Task {
                                        await vmModel.tryStartContainer(dockerRecord)
                                        await onRefresh()
                                    }
                                }) {
                                    Text("Turn On")
                                    .padding(.horizontal, 4)
                                }
                                .buttonStyle(.borderedProminent)
                                .keyboardShortcut(.defaultAction)
                                .controlSize(.large)
                            }
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
                NSLog("refresh: docker task")
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