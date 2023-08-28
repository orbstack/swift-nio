//
// Created by Danny Lin on 5/6/23.
//

import Foundation
import SwiftUI
import Defaults

struct DockerComposeGroupItem: View, Equatable, BaseDockerContainerItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    @Default(.tipsContainerDomainsShow) private var tipsContainerDomainsShow

    var composeGroup: ComposeGroup
    var selection: Set<DockerContainerId>
    var isFirstInList: Bool

    static func == (lhs: DockerComposeGroupItem, rhs: DockerComposeGroupItem) -> Bool {
        lhs.composeGroup == rhs.composeGroup &&
                lhs.selection == rhs.selection
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
                            actionInProgress: actionInProgress?.isStartStop == true) {
                        finishStop()
                    }
                    .disabled(actionInProgress != nil)
                    .help("Stop project")
                } else {
                    ProgressIconButton(systemImage: "play.fill",
                            actionInProgress: actionInProgress?.isStartStop == true) {
                        finishStart()
                    }
                    .disabled(actionInProgress != nil)
                    .help("Start project")
                }

                ProgressIconButton(systemImage: "trash.fill",
                        actionInProgress: actionInProgress == .delete) {
                    finishDelete()
                }
                .disabled(actionInProgress != nil)
                .help("Delete project")
            }
        }
                .padding(.vertical, 4)
                // ideally use Introspect to expand row, but does nothing for now
                /*
                .onDoubleClick {
                    presentPopover = true
                }
                 */
                .contextMenu {
                    Group {
                        Button("Start") {
                            finishStart()
                        }.disabled(actionInProgress != nil || isRunning)

                        Button("Stop") {
                            finishStop()
                        }.disabled(actionInProgress != nil || !isRunning)

                        Button("Restart") {
                            finishRestart()
                        }.disabled(actionInProgress != nil || !isRunning)

                        Button("Delete") {
                            finishDelete()
                        }.disabled(actionInProgress != nil)

                        Button("Kill") {
                            finishKill()
                        }.disabled((actionInProgress != nil && actionInProgress != .stop) || !isRunning)
                    }

                    Divider()

                    Button("Show Logs", action: showLogs)

                    Divider()

                    Group {
                        Button(action: {
                            let pasteboard = NSPasteboard.general
                            pasteboard.clearContents()
                            pasteboard.setString(composeGroup.project, forType: .string)
                        }) {
                            Label("Copy Name", systemImage: "doc.on.doc")
                        }
                    }
                }
    }

    private func showLogs() {
        if !vmModel.openDockerLogWindowIds.contains(.compose(project: composeGroup.project)) {
            // workaround: url can't contain "domain"???
            let projectB64URL = composeGroup.project.data(using: .utf8)!.base64URLEncodedString()
            NSWorkspace.shared.open(URL(string: "orbstack://docker/project-logs/\(projectB64URL)?base64=true")!)
        } else {
            // find window by title and bring to front
            for window in NSApp.windows {
                if window.title == WindowTitles.projectLogs(composeGroup.project) {
                    window.makeKeyAndOrderFront(nil)
                    break
                }
            }
        }
    }

    var selfId: DockerContainerId {
        .compose(project: composeGroup.project)
    }
}
