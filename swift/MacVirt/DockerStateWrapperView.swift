//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerStateWrapperView<Content: View>: View {
    @Environment(\.controlActiveState) private var controlActiveState: ControlActiveState
    @EnvironmentObject private var vmModel: VmViewModel

    let content: ([DKContainer], ContainerRecord) -> Content
    let refreshAction: () async -> Void

    init(refreshAction: @escaping () async -> Void, @ViewBuilder content: @escaping ([DKContainer], ContainerRecord) -> Content) {
        self.content = content
        self.refreshAction = refreshAction
    }

    var body: some View {
        StateWrapperView {
            Group {
                if let machines = vmModel.containers,
                   let dockerRecord = machines.first(where: { $0.builtin && $0.name == "docker" }) {
                    if let containers = vmModel.dockerContainers,
                       dockerRecord.state != .stopped {
                        content(containers, dockerRecord)
                        .onChange(of: dockerRecord.running) { _ in
                            Task {
                                await refreshAction()
                            }
                        }
                    } else if dockerRecord.state == .stopped {
                        VStack {
                            Text("Docker is off")
                                    .font(.title)
                                    .foregroundColor(.secondary)
                            Button(action: {
                                Task {
                                    await vmModel.tryStartContainer(dockerRecord)
                                    await refreshAction()
                                }
                            }) {
                                Text("Turn on Docker")
                            }
                        }
                    } else {
                        ProgressView(label: {
                            Text("Loading")
                        })
                    }
                } else {
                    ProgressView(label: {
                        Text("Loading")
                    })
                }
            }
            .task {
                NSLog("refresh: docker task")
                await refreshAction()
            }
            .onChange(of: controlActiveState) { state in
                if state == .key {
                    Task {
                        await refreshAction()
                    }
                }
            }
        }
    }
}