//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

struct K8SServiceItemView: View, Equatable, BaseK8SResourceItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var navModel: MainNavViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var listModel: AKListModel

    var service: K8SService
    var selection: Set<K8SResourceId> {
        listModel.selection as! Set<K8SResourceId>
    }

    static func == (lhs: K8SServiceItemView, rhs: K8SServiceItemView) -> Bool {
        lhs.service == rhs.service
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
                    .foregroundColor(Color(hex: 0xFAFAFA))
                    .background(Circle().fill(color))
                    // rasterize so opacity works on it as one big image
                    .drawingGroup(opaque: false)
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

            if let urlStr = service.wrapURL(host: service.preferredDomain) {
                ProgressIconButton(
                    systemImage: "link",
                    actionInProgress: false
                ) {
                    if let url = URL(string: urlStr) {
                        NSWorkspace.shared.open(url)
                    }
                }
                .help("Open in Browser")
            }

            ProgressIconButton(
                systemImage: "trash.fill",
                actionInProgress: actionInProgress == .delete
            ) {
                finishDelete()
            }
            .disabled(actionInProgress != nil)
            .help("Delete Service")
        }
        .padding(.vertical, 8)
        .akListOnDoubleClick {
            navModel.expandInspector.send()
        }
        .akListContextMenu {
            Group {
                Button {
                    finishDelete()
                } label: {
                    Label("Delete", systemImage: "trash")
                }
                .disabled(actionInProgress != nil)
            }

            Divider()

            Group {
                Button {
                    if let urlStr = service.wrapURL(host: service.preferredDomain),
                        let url = URL(string: urlStr)
                    {
                        NSWorkspace.shared.open(url)
                    }
                } label: {
                    Label("Open in Browser", systemImage: "link")
                }
                .disabled(
                    !(service.canOpen && (service.hasLocalhost || vmModel.netBridgeAvailable)))
            }

            Divider()

            Group {
                Button {
                    NSPasteboard.copy(service.name)
                } label: {
                    Label("Copy Name", systemImage: "doc.on.doc")
                }

                Button {
                    NSPasteboard.copy(
                        service.wrapURL(host: service.preferredDomain)
                            ?? service.preferredDomainAndPort)
                } label: {
                    Label("Copy Domain", systemImage: "doc.on.doc")
                }.disabled(vmModel.config?.networkBridge == false)

                let clusterIP = service.spec.clusterIP
                Button {
                    if let clusterIP {
                        NSPasteboard.copy(clusterIP)
                    }
                } label: {
                    Label("Copy Cluster IP", systemImage: "doc.on.doc")
                }.disabled(clusterIP == nil)

                /*
                    let externalIP = service.externalIP
                    Button {
                        if let externalIP {
                            NSPasteboard.copy(externalIP)
                        }
                    } label: {
                        Label("Copy External IP", systemImage: "doc.on.doc")
                    }.disabled(externalIP == nil)
                    */
            }
        }
    }

    var selfId: K8SResourceId {
        service.id
    }
}
