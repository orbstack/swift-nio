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
            let isRunning = pod.uiState == .running

            DetailsSection("Info") {
                SimpleKvTable(longestLabel: "Address") {
                    let domain = pod.preferredDomain
                    let ipAddress = pod.status.podIP

                    SimpleKvTableRow("Status") {
                        Text(pod.statusStr)
                    }

                    SimpleKvTableRow("Restarts") {
                        Text("\(pod.restartCount)")
                    }

                    SimpleKvTableRow("Age") {
                        Text(pod.ageStr)
                    }

                    // needs to be running w/ ip to have domain
                    if let ipAddress,
                       let url = URL(string: "http://\(domain)")
                    {
                        SimpleKvTableRow("Address") {
                            if vmModel.netBridgeAvailable {
                                CopyableText(copyAs: domain) {
                                    CustomLink(domain, url: url)
                                }
                            } else {
                                CopyableText(ipAddress)
                            }
                        }
                    }
                }
            }

            if pod.status.containerStatuses?.isEmpty == false {
                DetailsSection("Containers") {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(pod.status.containerStatuses ?? []) { container in
                            if let name = container.name {
                                // TODO: link
                                Label {
                                    CopyableText(name)
                                } icon: {
                                    // icon = red/green status dot
                                    Image(nsImage: SystemImages.statusDot(isRunning: container.ready ?? false))
                                }
                            }
                        }
                    }
                }
            }

            DividedButtonStack {
                DividedRowButton {
                    pod.showLogs(windowTracker: windowTracker)
                } label: {
                    Label("Logs", systemImage: "doc.text.magnifyingglass")
                }

                if isRunning {
                    DividedRowButton {
                        pod.openInTerminal()
                    } label: {
                        Label("Terminal", systemImage: "terminal")
                    }
                }
            }
        }
    }
}
