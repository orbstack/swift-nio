//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerStateWrapperView<Content: View, T>: View {
    @Environment(\.controlActiveState) private var controlActiveState: ControlActiveState
    @EnvironmentObject private var vmModel: VmViewModel

    let keyPath: KeyPath<VmViewModel, T?>
    let content: (T, ContainerRecord) -> Content

    init(
        _ keyPath: KeyPath<VmViewModel, T?>,
        @ViewBuilder content: @escaping (T, ContainerRecord) -> Content
    ) {
        self.keyPath = keyPath
        self.content = content
    }

    var body: some View {
        // TODO: return verdict as enum and use switch{} to fix loading flicker
        StateWrapperView {
            if let dockerMachine = vmModel.dockerMachine {
                Group {
                    if let entities = vmModel[keyPath: keyPath],
                        dockerMachine.record.state != .stopped
                    {
                        content(entities, dockerMachine.record)
                    } else if dockerMachine.record.state == .stopped {
                        VStack(spacing: 16) {  // match ContentUnavailableViewCompat desc padding
                            ContentUnavailableViewCompat(
                                "Docker Disabled", systemImage: "shippingbox.fill")

                            Button(action: {
                                Task {
                                    await vmModel.tryStartContainer(dockerMachine.record)
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
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
            }
        }
    }
}
