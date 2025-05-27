//
// Created by Danny Lin on 8/28/23.
//

import Defaults
import Foundation
import SwiftUI

struct K8sIcon: View {
    var body: some View {
        let color = SystemColors.desaturate(Color(.systemBlue))
        Image(systemName: "helm")
            .resizable()
            .aspectRatio(contentMode: .fit)
            .frame(width: 16, height: 16)
            .padding(8)
            .foregroundColor(Color(hex: 0xFAFAFA))
            .background(Circle().fill(color))
            // rasterize so opacity works on it as one big image
            .drawingGroup(opaque: true)
    }
}

struct DockerK8sGroupItem: View, Equatable {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var group: DockerK8sGroup

    static func == (lhs: DockerK8sGroupItem, rhs: DockerK8sGroupItem) -> Bool {
        lhs.group == rhs.group
    }

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(.k8sGroup) != nil
        let isRunning = group.anyRunning

        HStack {
            HStack {
                K8sIcon()
                    .padding(.trailing, 8)

                VStack(alignment: .leading) {
                    Text("Kubernetes")
                        .font(.body)
                        .lineLimit(1)
                }
            }
            .opacity(isRunning ? 1 : 0.5)
            // padding for expand arrow
            .padding(.leading, 8)

            Spacer()

            if isRunning {
                ProgressIconButton(systemImage: "stop.fill", actionInProgress: actionInProgress) {
                    Task { @MainActor in
                        await actionTracker.with(cid: .k8sGroup, action: .stop) {
                            await vmModel.tryStartStopK8s(enable: false)
                        }
                    }
                }
                .help("Stop Kubernetes")
            } else {
                ProgressIconButton(systemImage: "play.fill", actionInProgress: actionInProgress) {
                    Task { @MainActor in
                        await actionTracker.with(cid: .k8sGroup, action: .start) {
                            await vmModel.tryStartStopK8s(enable: true)
                        }
                    }
                }
                .help("Start Kubernetes")
            }

            ProgressIconButton(systemImage: "gear", actionInProgress: false) {
                Defaults[.selectedTab] = .k8sPods
            }
            .help("Go to Pods")
        }
        .padding(.vertical, 8)
        .akListContextMenu {
            if isRunning {
                Button(action: {
                    Task {
                        await actionTracker.with(cid: .k8sGroup, action: .stop) {
                            await vmModel.tryStartStopK8s(enable: false)
                        }
                    }
                }) {
                    Label("Stop", systemImage: "")
                }
                .disabled(actionInProgress)
            } else {
                Button(action: {
                    Task {
                        await actionTracker.with(cid: .k8sGroup, action: .start) {
                            await vmModel.tryStartStopK8s(enable: true)
                        }
                    }
                }) {
                    Label("Start", systemImage: "")
                }
                .disabled(actionInProgress)
            }

            Divider()

            Button("Go to Pods") {
                Defaults[.selectedTab] = .k8sPods
            }

            Button("Go to Services") {
                Defaults[.selectedTab] = .k8sServices
            }
        }
    }
}
