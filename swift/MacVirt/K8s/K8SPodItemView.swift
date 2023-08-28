//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct K8SPodItemView: View, Equatable, BaseK8SResourceItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var pod: K8SPod
    var selection: Set<K8SResourceId>

    @State private var presentPopover = false

    static func == (lhs: K8SPodItemView, rhs: K8SPodItemView) -> Bool {
        lhs.pod == rhs.pod &&
                lhs.selection == rhs.selection
    }

    var body: some View {
        let isRunning = pod.running
        let actionInProgress = actionTracker.ongoingFor(selfId)

        HStack {
            HStack {
                // this way it's consistent
                let color = SystemColors.forString(pod.name)
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
                    Text(pod.name)
                    .font(.body)

                    // TODO: show deployment here
                    /*
                    Text(pod.image)
                    .font(.subheadline)
                    .foregroundColor(.secondary)
                    .truncationMode(.tail)
                    .lineLimit(1)
                     */
                }
            }
            .opacity(pod.running ? 1 : 0.5)
            // padding for expand arrow
            .padding(.leading, 8)

            Spacer()

            // WA: crash on macOS 12 without nested HStack
            HStack {
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
                .help("Delete Pod")
            }
        }
        .padding(.vertical, 4)
        .onDoubleClick {
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
                    pod.showLogs(vmModel: vmModel)
                }) {
                    Label("Show Logs", systemImage: "")
                }

                Button(action: {
                    pod.openInTerminal()
                }) {
                    Label("Open Terminal", systemImage: "")
                }
                .disabled(!isRunning)

                Button(action: {
                    NSWorkspace.shared.open(URL(string: "http://\(pod.preferredDomain)")!)
                }) {
                    Label("Open in Browser", systemImage: "")
                }
                .disabled(!isRunning || !vmModel.netBridgeAvailable)
            }

            Divider()

            Group {
                Menu("Copy") {
                    Button(action: {
                        NSPasteboard.copy(pod.name)
                    }) {
                        Label("Name", systemImage: "")
                    }

                    Button(action: {
                        NSPasteboard.copy(pod.preferredDomain)
                    }) {
                        Label("Domain", systemImage: "")
                    }.disabled(vmModel.config?.networkBridge == false)

                    let ipAddress = pod.status.podIP
                    Button(action: {
                        if let ipAddress {
                            NSPasteboard.copy(ipAddress)
                        }
                    }) {
                        Label("IP", systemImage: "")
                    }.disabled(ipAddress == nil)
                }
            }
        }
    }

    private var detailsView: some View {
        VStack(alignment: .leading, spacing: 20) {
            let isRunning = pod.running

            VStack(alignment: .leading, spacing: 4) {
                Text("Info")
                .font(.headline)
                HStack(spacing: 12) {
                    let domain = pod.preferredDomain
                    let ipAddress = pod.status.podIP

                    VStack(alignment: .trailing) {
                        Text("Status")
                        Text("Restarts")
                        Text("Age")
                        if ipAddress != nil {
                            Text("Address")
                        }
                    }

                    VStack(alignment: .leading) {
                        Text(pod.statusStr)
                        .textSelection(.enabled)
                        Text("\(pod.restartCount)")
                        .textSelection(.enabled)
                        Text(pod.ageStr)
                        .textSelection(.enabled)
                        // needs to be running w/ ip to have domain
                        if let ipAddress {
                            if vmModel.netBridgeAvailable {
                                CustomLink(domain, url: URL(string: "http://\(domain)")!)
                            } else {
                                Text(ipAddress)
                                .textSelection(.enabled)
                            }
                        }
                    }
                }
                .padding(.leading, 16)
            }

            if pod.status.containerStatuses?.isEmpty == false {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Containers")
                    .font(.headline)
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(pod.status.containerStatuses ?? []) { container in
                            if let name = container.name {
                                Label {
                                    Text(name)
                                } icon: {
                                    // icon = red/green status dot
                                    Image(nsImage: SystemImages.redGreenDot(isRunning: container.ready ?? false))
                                }
                                .textSelection(.enabled)
                            }
                        }
                    }
                    .padding(.leading, 16)
                }
            }

            VStack(alignment: .leading) {
                HStack {
                    if isRunning {
                        Button {
                            pod.openInTerminal()
                        } label: {
                            Label("Terminal", systemImage: "terminal")
                        }
                        .controlSize(.large)
                    }

                    Button {
                        pod.showLogs(vmModel: vmModel)
                    } label: {
                        Label("Logs", systemImage: "doc.text.magnifyingglass")
                    }
                    .controlSize(.large)
                }
            }
        }
        .padding(20)
    }

    var selfId: K8SResourceId {
        pod.id
    }
}

protocol BaseK8SResourceItem {
    var vmModel: VmViewModel { get }
    var actionTracker: ActionTracker { get }

    var selection: Set<K8SResourceId> { get }

    var selfId: K8SResourceId { get }

    @MainActor
    func finishDelete()

    func isSelected() -> Bool
    @MainActor
    func resolveActionList() -> Set<K8SResourceId>
}

extension BaseK8SResourceItem {
    @MainActor
    func finishDelete() {
        for item in resolveActionList() {
            Task { @MainActor in
                await actionTracker.with(k8s: item, action: .delete) {
                    switch item {
                    case .pod:
                        await vmModel.tryK8sPodDelete(item)
                    case .service:
                        await vmModel.tryK8sServiceDelete(item)
                    default:
                        return
                    }
                }
            }
        }
    }

    func isSelected() -> Bool {
        selection.contains(selfId)
    }

    @MainActor
    func resolveActionList() -> Set<K8SResourceId> {
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        if isSelected() {
            // SwiftUI List bug: deleted items stay in selection set so we need to filter
            let firstPass = selection.filter { sel in
                switch sel {
                case .pod:
                    return vmModel.k8sPods?.contains { pod in pod.id == sel } ?? false
                case .service:
                    return vmModel.k8sServices?.contains { service in service.id == sel } ?? false
                default:
                    return false
                }
            }

            // now we only have items that exist
            // if we're doing a batch action, we could have deployments *and* other resources selected
            // TODO similar to docker, skip Pods/ReplicaSets under a Deployment/StatefulSet/DaemonSet if the higher-up item is selected
            return firstPass
        } else {
            return [selfId]
        }
    }
}