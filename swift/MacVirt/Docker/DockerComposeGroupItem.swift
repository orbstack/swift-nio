//
// Created by Danny Lin on 5/6/23.
//

import Defaults
import Foundation
import SwiftUI

struct DockerComposeGroupImage: View {
    let project: String

    var body: some View {
        let color = SystemColors.forString(project)
        Image(systemName: "square.stack.3d.up.fill")
            .resizable()
            .aspectRatio(contentMode: .fit)
            .frame(width: 32, height: 32)
            .foregroundColor(color)
    }
}

struct DockerComposeGroupItem: View, Equatable, BaseDockerContainerItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var listModel: AKListModel

    @State private var presentConfirmDelete = false

    @Default(.tipsContainerDomainsShow) private var tipsContainerDomainsShow

    var composeGroup: ComposeGroup
    var children: [DockerListItem]
    var selection: Set<DockerContainerId> {
        listModel.selection as! Set<DockerContainerId>
    }

    var isFirstInList: Bool

    static func == (lhs: DockerComposeGroupItem, rhs: DockerComposeGroupItem) -> Bool {
        lhs.composeGroup == rhs.composeGroup
    }

    var body: some View {
        let isRunning = composeGroup.anyRunning
        let actionInProgress = actionTracker.ongoingFor(selfId)
        let firstChild =
            if case let .container(container) = children.first {
                container
            } else {
                DKContainer?(nil)
            }

        HStack {
            HStack {
                DockerComposeGroupImage(project: composeGroup.project)
                    .padding(.trailing, 8)
                    .if(isFirstInList) {
                        $0.popover(isPresented: $tipsContainerDomainsShow, arrowEdge: .trailing) {
                            HStack {
                                Image(systemName: "network")
                                    .resizable()
                                    .frame(width: 32, height: 32)
                                    .foregroundColor(.accentColor)
                                    .padding(.trailing, 4)

                                VStack(alignment: .leading, spacing: 2) {
                                    Text("New: Domain names for services")
                                        .font(.headline)

                                    Text("See all containers at [orb.local](http://orb.local)")
                                        .font(.body)
                                        .foregroundColor(.secondary)
                                }
                            }
                            .padding(20)
                            .overlay(alignment: .topTrailing) {
                                Button {
                                    tipsContainerDomainsShow = false
                                } label: {
                                    Image(systemName: "xmark")
                                        .resizable()
                                        .frame(width: 8, height: 8)
                                        .foregroundColor(.secondary)
                                }
                                .buttonStyle(.plain)
                                .padding(8)
                            }
                        }
                    }

                VStack(alignment: .leading) {
                    Text(composeGroup.project)
                        .font(.body)
                        .lineLimit(1)
                }
            }
            .opacity(isRunning ? 1 : 0.5)
            // padding for expand arrow
            .padding(.leading, 8)

            Spacer()

            if isRunning {
                ProgressIconButton(
                    systemImage: "stop.fill",
                    actionInProgress: actionInProgress?.isStartStop == true
                ) {
                    finishStop()
                }
                .disabled(actionInProgress != nil || !composeGroup.isRealCompose)
                .help("Stop project")
            } else {
                ProgressIconButton(
                    systemImage: "play.fill",
                    actionInProgress: actionInProgress?.isStartStop == true
                ) {
                    finishStart()
                }
                .disabled(actionInProgress != nil || !composeGroup.isRealCompose)
                .help("Start project")
            }

            ProgressIconButton(
                systemImage: "trash.fill",
                actionInProgress: actionInProgress == .delete
            ) {
                presentConfirmDelete = true
            }
            .disabled(actionInProgress != nil || !composeGroup.isRealCompose)
            .help("Delete project")
        }
        .padding(.vertical, 8)
        // projects are always multiple containers, so no need to change msg
        .confirmationDialog(
            "Delete containers?",
            isPresented: $presentConfirmDelete
        ) {
            Button("Delete", role: .destructive) {
                finishDelete()
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .akListContextMenu {
            Group {
                Button {
                    finishStart()
                } label: {
                    Label("Start", systemImage: "play")
                }.disabled(actionInProgress != nil || isRunning || !composeGroup.isRealCompose)

                Button {
                    finishStop()
                } label: {
                    Label("Stop", systemImage: "stop")
                }.disabled(actionInProgress != nil || !isRunning || !composeGroup.isRealCompose)

                Button {
                    finishRestart()
                } label: {
                    Label("Restart", systemImage: "arrow.clockwise")
                }.disabled(actionInProgress != nil || !isRunning || !composeGroup.isRealCompose)

                if composeGroup.anyPaused {
                    Button {
                        finishUnpause()
                    } label: {
                        Label("Unpause", systemImage: "playpause")
                    }.disabled(
                        (actionInProgress != nil) || !composeGroup.isRealCompose)
                } else {
                    Button {
                        finishPause()
                    } label: {
                        Label("Pause", systemImage: "pause")
                    }.disabled(
                        (actionInProgress != nil) || !isRunning || !composeGroup.isRealCompose)
                }

                Button {
                    finishKill()
                } label: {
                    Label("Kill", systemImage: "xmark.octagon")
                }.disabled(
                    (actionInProgress != nil && actionInProgress != .stop) || !isRunning
                        || !composeGroup.isRealCompose)

                Button {
                    presentConfirmDelete = true
                } label: {
                    Label("Delete", systemImage: "trash")
                }.disabled(actionInProgress != nil || !composeGroup.isRealCompose)
            }

            Divider()

            let projectPath = firstChild?.composeConfigFiles?.first
            Group {
                Button {
                    composeGroup.showLogs(windowTracker: windowTracker)
                } label: {
                    Label("Logs", systemImage: "doc.text.magnifyingglass")
                }

                Button {
                    if let projectPath {
                        let parentDir = URL(fileURLWithPath: projectPath)
                            .deletingLastPathComponent().path
                        NSWorkspace.shared.selectFile(
                            projectPath, inFileViewerRootedAtPath: parentDir)
                    }
                } label: {
                    Label("Show in Finder", systemImage: "folder")
                }.disabled(projectPath == nil)
            }

            Divider()

            Group {
                Button {
                    NSPasteboard.copy(composeGroup.project)
                } label: {
                    Label("Copy Name", systemImage: "doc.on.doc")
                }

                Button {
                    if let projectPath {
                        NSPasteboard.copy(projectPath)
                    }
                } label: {
                    Label("Copy Path", systemImage: "doc.on.doc")
                }.disabled(projectPath == nil)
            }
        }
    }

    var selfId: DockerContainerId {
        .compose(project: composeGroup.project)
    }
}
