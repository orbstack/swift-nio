//
// Created by Danny Lin on 5/6/23.
//

import Defaults
import Foundation
import SwiftUI

struct DockerComposeGroupItem: View, Equatable, BaseDockerContainerItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var listModel: AKListModel
    
    @State private var presentConfirmDelete = false

    @Default(.tipsContainerDomainsShow) private var tipsContainerDomainsShow

    var composeGroup: ComposeGroup
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

        HStack {
            HStack {
                let color = SystemColors.forString(composeGroup.project)
                Image(systemName: "square.stack.3d.up.fill")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 32, height: 32)
                    .padding(.trailing, 8)
                    .foregroundColor(color)
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
                                Button(action: {
                                    tipsContainerDomainsShow = false
                                }) {
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

            // crash on macOS 12 without nested HStack
            // 0.7 scale also crashes - 0.75 is ok
            HStack {
                if isRunning {
                    ProgressIconButton(systemImage: "stop.fill",
                                       actionInProgress: actionInProgress?.isStartStop == true)
                    {
                        finishStop()
                    }
                    .disabled(actionInProgress != nil || !composeGroup.isFullCompose)
                    .help("Stop project")
                } else {
                    ProgressIconButton(systemImage: "play.fill",
                                       actionInProgress: actionInProgress?.isStartStop == true)
                    {
                        finishStart()
                    }
                    .disabled(actionInProgress != nil || !composeGroup.isFullCompose)
                    .help("Start project")
                }

                ProgressIconButton(systemImage: "trash.fill",
                                   actionInProgress: actionInProgress == .delete)
                {
                    presentConfirmDelete = true
                }
                .disabled(actionInProgress != nil || !composeGroup.isFullCompose)
                .help("Delete project")
            }
        }
        .padding(.vertical, 8)
        // projects are always multiple containers, so no need to change msg
        .confirmationDialog("Delete containers?",
                            isPresented: $presentConfirmDelete)
        {
            Button("Delete", role: .destructive) {
                finishDelete()
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .akListContextMenu {
            Group {
                Button("Start") {
                    finishStart()
                }.disabled(actionInProgress != nil || isRunning || !composeGroup.isFullCompose)

                Button("Stop") {
                    finishStop()
                }.disabled(actionInProgress != nil || !isRunning || !composeGroup.isFullCompose)

                Button("Restart") {
                    finishRestart()
                }.disabled(actionInProgress != nil || !isRunning || !composeGroup.isFullCompose)

                Button("Delete") {
                    presentConfirmDelete = true
                }.disabled(actionInProgress != nil || !composeGroup.isFullCompose)

                Button("Kill") {
                    finishKill()
                }.disabled((actionInProgress != nil && actionInProgress != .stop) || !isRunning || !composeGroup.isFullCompose)
            }

            Divider()

            Button("Show Logs") {
                composeGroup.showLogs(windowTracker: windowTracker)
            }

            Divider()

            Button(action: {
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(composeGroup.project, forType: .string)
            }) {
                Label("Copy Name", systemImage: "doc.on.doc")
            }
        }
    }

    var selfId: DockerContainerId {
        .compose(project: composeGroup.project)
    }
}
