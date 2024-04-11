//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct K8SServiceDetails: View {
    @EnvironmentObject var vmModel: VmViewModel

    let service: K8SService

    var body: some View {
        DetailsStack {
            DetailsSection("Info") {
                SimpleKvTable(longestLabel: "Cluster IP") {
                    let domain = service.preferredDomain
                    let clusterIP = service.spec.clusterIP
                    // redundant. our external ip is always the same as node
                    // let externalIP = service.externalIP
                    let address = service.wrapURL(host: domain) ?? service.preferredDomainAndPort
                    let addressVisible = service.wrapURLNoScheme(host: domain) ?? service.preferredDomainAndPort
                    let isWebService = service.isWebService

                    SimpleKvTableRow("Type") {
                        Text(service.spec.type.rawValue)
                    }

                    SimpleKvTableRow("Age") {
                        Text(service.ageStr)
                    }

                    SimpleKvTableRow("Cluster IP") {
                        if let clusterIP {
                            CopyableText(clusterIP)
                        }
                    }

                    if let url = URL(string: address) {
                        SimpleKvTableRow("Domain") {
                            if isWebService {
                                CopyableText(copyAs: service.preferredDomainAndPort) {
                                    CustomLink(addressVisible, url: url)
                                }
                            } else {
                                CopyableText(addressVisible)
                            }
                        }
                    }
                }
            }

            if service.spec.ports?.isEmpty == false {
                DetailsSection("Ports") {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(service.spec.ports ?? []) { port in
                            // TODO: dedupe logic
                            let portNumber = service.spec.type == .loadBalancer ? port.port : (port.nodePort ?? port.port)
                            // avoid pretty commas num format
                            if port.proto != "TCP" {
                                CopyableText("\(String(portNumber))/\(port.proto ?? "TCP")")
                            } else {
                                CopyableText(String(portNumber))
                            }
                        }
                    }
                }
            }
        }
    }
}
