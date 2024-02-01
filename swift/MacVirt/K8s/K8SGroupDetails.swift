//
// Created by Danny Lin on 2/1/24.
//

import Foundation
import SwiftUI

struct K8SGroupDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker

    var body: some View {
        DetailsStack {
            DetailsSection("Kubernetes Pods") {
                let pods = vmModel.k8sPods ?? []

                VStack(alignment: .leading, spacing: 4) {
                    ForEach(pods) { pod in
                        Label {
                            CopyableText(pod.name)
                            .lineLimit(1)
                        } icon: {
                            // icon = red/green status dot
                            Image(nsImage: SystemImages.statusDot(isRunning: pod.statusStr == "Running"))
                        }
                    }
                }
            }
        }
    }
}
