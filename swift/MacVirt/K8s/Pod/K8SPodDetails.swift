//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct K8SPodDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker

    let pod: K8SPod

    var body: some View {
        DetailsStack {
            DetailsKvSection {
                let domain = pod.preferredDomain
                let ipAddress = pod.status.podIP

                DetailsRow("Status", text: pod.statusStr)
                DetailsRow("Restarts", text: "\(pod.restartCount)")
                DetailsRow("Age", text: pod.ageStr)

                // needs to be running w/ ip to have domain
                if let ipAddress,
                    let url = URL(string: "http://\(domain)")
                {
                    if vmModel.netBridgeAvailable {
                        DetailsRow("IP") {
                            CopyableText(copyAs: domain) {
                                CustomLink(domain, url: url)
                            }
                        }
                    } else {
                        DetailsRow("IP", copyableText: ipAddress)
                    }
                }
            }

            if pod.status.containerStatuses?.isEmpty == false {
                DetailsListSection("Containers") {
                    ForEach(pod.status.containerStatuses ?? []) { container in
                        if let name = container.name {
                            // TODO: link
                            Label {
                                CopyableText(name)
                            } icon: {
                                // icon = red/green status dot
                                Image(
                                    nsImage: SystemImages.statusDot(
                                        isRunning: container.ready ?? false))
                            }
                        }
                    }
                }
            }

            if pod.status.ephemeralContainerStatuses?.isEmpty == false {
                DetailsListSection("Ephemeral Containers") {
                    ForEach(pod.status.ephemeralContainerStatuses ?? []) { container in
                        if let name = container.name {
                            // TODO: link
                            Label {
                                CopyableText(name)
                            } icon: {
                                // icon = red/green status dot
                                Image(
                                    nsImage: SystemImages.statusDot(
                                        isRunning: container.ready ?? false))
                            }
                        }
                    }
                }
            }
        }
    }
}
