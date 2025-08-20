//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

struct K8SPodItemView: View, Equatable, BaseK8SResourceItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var navModel: MainNavViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var listModel: AKListModel

    var pod: K8SPod
    var selection: Set<K8SResourceId> {
        listModel.selection as! Set<K8SResourceId>
    }

    static func == (lhs: K8SPodItemView, rhs: K8SPodItemView) -> Bool {
        lhs.pod == rhs.pod
    }

    var body: some View {
        let state = pod.uiState
        let actionInProgress = actionTracker.ongoingFor(selfId)

        HStack {
            HStack {
                // this way it's consistent. we use red for error so it's confusing otherwise
                let color = SystemColors.desaturate(Color(.systemBlue))
                Group {
                    switch state {
                    case .running, .completed:
                        Image(systemName: "helm")
                            .resizable()
                            .aspectRatio(contentMode: .fit)
                            .frame(width: 16, height: 16)
                            .padding(6)
                            .foregroundColor(Color(hex: 0xFAFAFA))
                            .background(Circle().fill(color))
                            // rasterize so opacity works on it as one big image
                            .compositingGroup()

                    case .loading:
                        // can't rasterize this so only do opacity on bg
                        ProgressView()
                            .scaleEffect(0.5)
                            .aspectRatio(contentMode: .fit)
                            .frame(width: 16, height: 16)
                            .padding(6)
                            .foregroundColor(Color(hex: 0xFAFAFA))
                            .background(Circle().fill(color).opacity(0.5))

                    case .error:
                        Image(systemName: "exclamationmark")
                            .resizable()
                            .aspectRatio(contentMode: .fit)
                            .frame(width: 16, height: 16)
                            .padding(6)
                            .foregroundColor(Color(hex: 0xFAFAFA))
                            .background(Circle().fill(SystemColors.desaturate(Color(.systemRed))))
                            // rasterize so opacity works on it as one big image
                            .compositingGroup()
                    }
                }
                .opacity((state == .completed) ? 0.5 : 1)

                VStack(alignment: .leading) {
                    Text(pod.name)
                        .font(.body)
                        .lineLimit(1)

                    // TODO: show deployment here
                    /*
                     Text(pod.image)
                         .font(.subheadline)
                         .foregroundColor(.secondary)
                         .truncationMode(.tail)
                         .lineLimit(1)
                         .help(pod.image)
                      */
                }
                .opacity((state == .loading || state == .completed) ? 0.5 : 1)
            }

            Spacer()

            ProgressButtonRow {
                ProgressIconButton(
                    systemImage: "trash.fill",
                    actionInProgress: actionInProgress == .delete
                ) {
                    finishDelete()
                }
                .disabled(actionInProgress != nil)
                .help("Delete")
            }
        }
        .padding(.vertical, 4)
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
                    pod.showLogs(windowTracker: windowTracker)
                } label: {
                    Label("Logs", systemImage: "doc.text.magnifyingglass")
                }

                Button {
                    pod.openInTerminal()
                } label: {
                    Label("Terminal", systemImage: "terminal")
                }
                .disabled(state != .running)

                Button {
                    if let url = URL(string: "http://\(pod.preferredDomain)") {
                        NSWorkspace.shared.open(url)
                    }
                } label: {
                    Label("Open in Browser", systemImage: "link")
                }
                .disabled(state != .running || !vmModel.netBridgeAvailable)
            }

            Divider()

            Group {
                Button {
                    NSPasteboard.copy(pod.name)
                } label: {
                    Label("Copy Name", systemImage: "doc.on.doc")
                }

                Button {
                    NSPasteboard.copy(pod.preferredDomain)
                } label: {
                    Label("Copy Domain", systemImage: "doc.on.doc")
                }.disabled(vmModel.config?.networkBridge == false)

                let ipAddress = pod.status.podIP
                Button {
                    if let ipAddress {
                        NSPasteboard.copy(ipAddress)
                    }
                } label: {
                    Label("Copy IP", systemImage: "doc.on.doc")
                }.disabled(ipAddress == nil)
            }
        }
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
            // if we're doing a batch action, we could have deployments *and* other resources selected
            // TODO: similar to docker, skip Pods/ReplicaSets under a Deployment/StatefulSet/DaemonSet if the higher-up item is selected
            return selection
        } else {
            return [selfId]
        }
    }
}
