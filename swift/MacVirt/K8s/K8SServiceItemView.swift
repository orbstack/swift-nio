//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct K8SServiceItemView: View, Equatable, BaseK8SResourceItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var service: K8SService
    var selection: Set<K8SResourceId>

    @State private var presentPopover = false

    static func == (lhs: K8SServiceItemView, rhs: K8SServiceItemView) -> Bool {
        lhs.service == rhs.service &&
                lhs.selection == rhs.selection
    }

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(selfId)

        HStack {
            HStack {
                // this way it's consistent
                let color = SystemColors.desaturate(Color(service.spec.type.uiColor))
                Image(systemName: service.systemImage)
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
                    Text(service.name)
                        .font(.body)
                        .lineLimit(1)

                    // TODO: show deployment here
                    /*
                    Text(service.image)
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .truncationMode(.tail)
                        .lineLimit(1)
                     */
                }
            }
            // padding for expand arrow
            .padding(.leading, 8)

            Spacer()

            // WA: crash on macOS 12 without nested HStack
            HStack {
                if let urlStr = service.wrapURL(host: service.preferredDomain) {
                    ProgressIconButton(systemImage: "link",
                            actionInProgress: false) {
                        if let url = URL(string: urlStr) {
                            NSWorkspace.shared.open(url)
                        }
                    }
                    .help("Open in Browser")
                }

                ProgressIconButton(systemImage: "info.circle.fill",
                        actionInProgress: false) {
                    presentPopover = true
                }
                .help("Get Info")
                .popover(isPresented: $presentPopover, arrowEdge: .leading) {
                    detailsView
                }

                ProgressIconButton(systemImage: "trash.fill",
                        actionInProgress: actionInProgress == .delete) {
                    finishDelete()
                }
                .disabled(actionInProgress != nil)
                .help("Delete Service")
            }
        }
        .padding(.vertical, 4)
        .onRawDoubleClick {
            presentPopover = true
        }
        .contextMenu {
            Group {
                Button(action: {
                    finishDelete()
                }) {
                    Label("Delete", systemImage: "")
                }
                .disabled(actionInProgress != nil)
            }

            Divider()

            Group {
                Button(action: {
                    presentPopover = true
                }) {
                    Label("Get Info", systemImage: "")
                }

                Button(action: {
                    if let urlStr = service.wrapURL(host: service.preferredDomain),
                          let url = URL(string: urlStr) {
                        NSWorkspace.shared.open(url)
                    }
                }) {
                    Label("Open in Browser", systemImage: "")
                }
                .disabled(!(service.canOpen && (service.hasLocalhost || vmModel.netBridgeAvailable)))
            }

            Divider()

            Group {
                Menu("Copy") {
                    Button(action: {
                        NSPasteboard.copy(service.name)
                    }) {
                        Label("Name", systemImage: "")
                    }

                    Button(action: {
                        NSPasteboard.copy(service.wrapURL(host: service.preferredDomain) ??
                                service.preferredDomainAndPort)
                    }) {
                        Label("Address", systemImage: "")
                    }.disabled(vmModel.config?.networkBridge == false)

                    let clusterIP = service.spec.clusterIP
                    Button(action: {
                        if let clusterIP {
                            NSPasteboard.copy(clusterIP)
                        }
                    }) {
                        Label("Cluster IP", systemImage: "")
                    }.disabled(clusterIP == nil)

                    /*
                    let externalIP = service.externalIP
                    Button(action: {
                        if let externalIP {
                            NSPasteboard.copy(externalIP)
                        }
                    }) {
                        Label("External IP", systemImage: "")
                    }.disabled(externalIP == nil)
                     */
                }
            }
        }
    }

    private var detailsView: some View {
        VStack(alignment: .leading, spacing: 20) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Info")
                .font(.headline)
                HStack(spacing: 12) {
                    let domain = service.preferredDomain
                    let clusterIP = service.spec.clusterIP
                    // redundant. our external ip is always the same as node
                    //let externalIP = service.externalIP
                    let address = service.wrapURL(host: domain) ?? service.preferredDomainAndPort
                    let addressVisible = service.wrapURLNoScheme(host: domain) ?? service.preferredDomainAndPort
                    let isWebService = service.isWebService

                    VStack(alignment: .trailing) {
                        Text("Type")
                        Text("Age")
                        if clusterIP != nil {
                            Text("Cluster IP")
                        }
                        /*
                        if externalIP != nil {
                            Text("External IP")
                        }
                        */
                        Text("Address")
                    }

                    VStack(alignment: .leading) {
                        Text(service.spec.type.rawValue)
                        .textSelectionWithWorkaround()
                        Text(service.ageStr)
                        .textSelectionWithWorkaround()
                        if let clusterIP {
                            Text(clusterIP)
                            .textSelectionWithWorkaround()
                        }
                        /*
                        if let externalIP {
                            Text(externalIP)
                            .textSelectionWithWorkaround()
                        }
                         */
                        if let url = URL(string: address) {
                            if isWebService {
                                CustomLink(addressVisible, url: url)
                            } else {
                                Text(addressVisible)
                                .textSelectionWithWorkaround()
                            }
                        }
                    }
                }
                .padding(.leading, 16)
            }

            if service.spec.ports?.isEmpty == false {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Ports")
                    .font(.headline)
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(service.spec.ports ?? []) { port in
                            // TODO dedupe logic
                            let portNumber = service.spec.type == .loadBalancer ? port.port : (port.nodePort ?? port.port)
                            // avoid pretty commas num format
                            if port.proto != "TCP" {
                                Text("\(String(portNumber))/\(port.proto ?? "TCP")")
                                .textSelectionWithWorkaround()
                            } else {
                                Text(String(portNumber))
                                .textSelectionWithWorkaround()
                            }
                        }
                    }
                    .padding(.leading, 16)
                }
            }
        }
        .padding(20)
    }

    var selfId: K8SResourceId {
        service.id
    }
}
