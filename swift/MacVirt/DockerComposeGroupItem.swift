//
// Created by Danny Lin on 5/6/23.
//

import Foundation
import SwiftUI

struct DockerComposeGroupItem: View, Equatable, BaseDockerContainerItem {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var composeGroup: ComposeGroup
    var selection: Set<DockerContainerId>

    static func == (lhs: DockerComposeGroupItem, rhs: DockerComposeGroupItem) -> Bool {
        lhs.composeGroup == rhs.composeGroup &&
                lhs.selection == rhs.selection
    }

    var body: some View {
        let isRunning = composeGroup.anyRunning
        let actionInProgress = actionTracker.ongoingFor(selfId)

        HStack {
            HStack {
                let color = SystemColors.forHashable(composeGroup.project)
                Image(systemName: "tray.full.fill")
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 32, height: 32)
                        .padding(.trailing, 8)
                        .foregroundColor(color)

                VStack(alignment: .leading) {
                    Text(composeGroup.project)
                            .font(.body)
                }
            }
                    .opacity(isRunning ? 1 : 0.5)

            Spacer()

            if isRunning {
                Button(action: {
                    finishStop()
                }) {
                    let opacity = actionInProgress?.isStartStop == true ? 1.0 : 0.0
                    ZStack {
                        Image(systemName: "stop.fill")
                                .opacity(1 - opacity)

                        ProgressView()
                                .scaleEffect(0.75)
                                .opacity(opacity)
                                .frame(maxWidth: 32.142857*0.7, maxHeight: 32.142857*0.7)
                    }
                }
                        .buttonStyle(.borderless)
                        .disabled(actionInProgress != nil)
                        .help("Stop project")
            } else {
                Button(action: {
                    finishStart()
                }) {
                    let opacity = actionInProgress?.isStartStop == true ? 1.0 : 0.0
                    ZStack {
                        Image(systemName: "play.fill")
                                .opacity(1 - opacity)

                        ProgressView()
                                .scaleEffect(0.75)
                                .opacity(opacity)
                                .frame(maxWidth: 32.142857*0.7, maxHeight: 32.142857*0.7)
                    }
                }
                        .buttonStyle(.borderless)
                        .disabled(actionInProgress != nil)
                        .help("Start project")
            }

            Button(action: {
                finishRemove()
            }) {
                let opacity = actionInProgress == .remove ? 1.0 : 0.0
                ZStack {
                    Image(systemName: "trash.fill")
                            .opacity(1 - opacity)

                    ProgressView()
                            .scaleEffect(0.75)
                            .opacity(opacity)
                            .frame(maxWidth: 32.142857*0.7, maxHeight: 32.142857*0.7)
                }
            }
                    .buttonStyle(.borderless)
                    .disabled(actionInProgress != nil)
                    .help("Delete project")
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
                        Button(action: {
                            finishStart()
                        }) {
                            Label("Start", systemImage: "start.fill")
                        }.disabled(actionInProgress != nil || isRunning)

                        Button(action: {
                            finishStop()
                        }) {
                            Label("Stop", systemImage: "stop.fill")
                        }.disabled(actionInProgress != nil || !isRunning)

                        Button(action: {
                            finishRestart()
                        }) {
                            Label("Restart", systemImage: "arrow.clockwise")
                        }.disabled(actionInProgress != nil || !isRunning)

                        Button(action: {
                            finishRemove()
                        }) {
                            Label("Delete", systemImage: "trash.fill")
                        }.disabled(actionInProgress != nil)
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
        if !vmModel.openLogWindowIds.contains(composeGroup.project) {
            NSWorkspace.shared.open(URL(string: "orbstack://docker/projects/logs/\(composeGroup.project)")!)
        } else {
            // find window by title and bring to front
            for window in NSApp.windows {
                if window.title == "Project Logs: \(composeGroup.project)" {
                    window.makeKeyAndOrderFront(nil)
                    break
                }
            }
        }
    }

    var selfId: DockerContainerId {
        .compose(project: composeGroup.project,
                configFiles: composeGroup.configFiles)
    }
}