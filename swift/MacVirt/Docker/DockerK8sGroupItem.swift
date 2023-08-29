//
// Created by Danny Lin on 8/28/23.
//

import Foundation
import SwiftUI
import Defaults

struct DockerK8sGroupItem: View, Equatable {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var group: DockerK8sGroup

    static func == (lhs: DockerK8sGroupItem, rhs: DockerK8sGroupItem) -> Bool {
        lhs.group == rhs.group
    }

    var body: some View {
        let isRunning = group.anyRunning

        HStack {
            HStack {
                let color = SystemColors.desaturate(Color(.systemBlue))
                Image(systemName: "helm")
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(width: 16, height: 16)
                .padding(8)
                .foregroundColor(Color(hex: 0xfafafa))
                .background(Circle().fill(color))
                // rasterize so opacity works on it as one big image
                .drawingGroup(opaque: true)
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

            ProgressIconButton(systemImage: "gear", actionInProgress: false) {
                Defaults[.selectedTab] = "k8s-pods"
            }
            .help("Go to Pods")
        }
        .padding(.vertical, 4)
        .contextMenu {
            Button("Go to Pods") {
                Defaults[.selectedTab] = "k8s-pods"
            }

            Button("Go to Services") {
                Defaults[.selectedTab] = "k8s-services"
            }
        }
    }
}
