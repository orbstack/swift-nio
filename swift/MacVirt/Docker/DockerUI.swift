//
// Created by Danny Lin on 5/20/23.
//

import Foundation
import AppKit
import Defaults

extension DKContainer {
    func openInTerminal() {
        Task {
            do {
                try await openTerminal(AppConfig.dockerExe, ["exec", "-it", id, "sh"])
            } catch {
                NSLog("Open terminal failed: \(error)")
            }
        }
    }

    @MainActor
    func showLogs(vmModel: VmViewModel) {
        if !vmModel.openLogWindowIds.contains(.container(id: id)) {
            NSWorkspace.shared.open(URL(string: "orbstack://docker/container-logs/\(id)")!)
        } else {
            // find window by title and bring to front
            for window in NSApp.windows {
                if window.title == WindowTitles.containerLogs(userName) {
                    window.makeKeyAndOrderFront(nil)
                    break
                }
            }
        }
    }

    func copyRunCommand() async {
        do {
            let runCmd = try await runProcessChecked(AppConfig.dockerExe,
                    ["inspect", "--format", DKInspectRunCommandTemplate, id],
                    env: ["DOCKER_HOST": "unix://\(Files.dockerSocket)"])

            NSPasteboard.copy(runCmd)
        } catch {
            NSLog("Failed to get run command: \(error)")
        }
    }
}

extension DKPort {
    var formatted: String {
        let ctrPort = privatePort
        let localPort = publicPort ?? privatePort
        let protoSuffix = type == "tcp" ? "" : "  (\(type.uppercased()))"
        let portStr = ctrPort == localPort ? "\(ctrPort)" : "\(ctrPort) → \(localPort)"

        return "\(portStr)\(protoSuffix)"
    }

    func openUrl() {
        let ctrPort = privatePort
        let localPort = publicPort ?? privatePort
        let httpProto = (ctrPort == 443 || ctrPort == 8443 || localPort == 443 || localPort == 8443) ? "https" : "http"
        NSWorkspace.shared.open(URL(string: "\(httpProto)://localhost:\(localPort)")!)
    }
}

extension DKMountPoint {
    var formatted: String {
        let src = source
        let dest = destination

        if let volName = name,
           type == .volume {
            return "\(abbreviateMount(volName))  →  \(dest)"
        } else {
            let home = FileManager.default.homeDirectoryForCurrentUser.path
            let prettySrc = src.replacingOccurrences(of: home, with: "~")
            return "\(abbreviateMount(prettySrc))  →  \(dest)"
        }
    }

    func openSourceDirectory() {
        if let volName = name,
           type == .volume {
            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: "\(Folders.nfsDockerVolumes)/\(volName)")
        } else {
            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: source)
        }
    }
}

private func abbreviateMount(_ src: String) -> String {
    if src.count > 45 {
        return src.prefix(35) + "…" + src.suffix(10)
    } else {
        return src
    }
}

extension NSPasteboard {
    static func copy(_ string: String) {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(string, forType: .string)
    }

    static func copy(data: Data) {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setData(data, forType: .string)
    }
}

struct WindowTitles {
    static let projectLogsBase = "Project Logs"
    static func projectLogs(_ project: String?) -> String {
        if let project {
            return "\(project) — \(projectLogsBase)"
        } else {
            return projectLogsBase
        }
    }

    static let containerLogsBase = "Logs"
    static func containerLogs(_ name: String?) -> String {
        if let name {
            return "\(name) — \(containerLogsBase)"
        } else {
            return containerLogsBase
        }
    }
}

enum DockerListItem: Identifiable, Equatable {
    case builtinRecord(ContainerRecord)
    case sectionLabel(String)
    case container(DKContainer)
    case compose(ComposeGroup, children: [DockerListItem])

    var id: DockerContainerId {
        switch self {
        case .builtinRecord(let record):
            return .builtinRecord(record.id)
        case .sectionLabel(let label):
            return .sectionLabel(label)
        case .container(let container):
            return .container(id: container.id)
        case .compose(let group, _):
            return .compose(project: group.project)
        }
    }

    var containerName: String {
        switch self {
        case .container(let container):
            return container.names.first ?? ""
        case .compose(let group, _):
            return group.project
        default:
            return ""
        }
    }

    var isGroup: Bool {
        switch self {
        case .compose:
            return true
        default:
            return false
        }
    }

    var listChildren: [DockerListItem]? {
        switch self {
        case .compose(_, let children):
            return children
        default:
            return nil
        }
    }
}

struct DockerContainerLists {
    static func makeListItems(filteredContainers: [DKContainer],
                       dockerRecord: ContainerRecord? = nil,
                       showStopped: Bool) -> [DockerListItem] {
        // TODO - workaround was to remove section headers
        var listItems: [DockerListItem] = [
            //DockerListItem(builtinRecord: dockerRecord),
            //DockerListItem(sectionLabel: "Running"),
        ]
        var runningItems: [DockerListItem] = []
        var stoppedItems: [DockerListItem] = []

        // collect compose groups and remove them from containers
        var ungroupedContainers: [DKContainer] = []
        var composeGroups: [ComposeGroup: [DKContainer]] = [:]

        for container in filteredContainers {
            if let composeProject = container.composeProject {
                let group = ComposeGroup(project: composeProject)
                if composeGroups[group] == nil {
                    composeGroups[group] = [container]
                } else {
                    composeGroups[group]?.append(container)
                }
            } else {
                ungroupedContainers.append(container)
            }
        }

        // convert to list items
        for (var group, var containers) in composeGroups {
            // sort
            containers.sort { a, b in
                a.userName < b.userName
            }

            let children = containers.map { DockerListItem.container($0) }
            // if ANY container in the group is running, show the group as running
            let anyRunning = containers.contains(where: { $0.running })
            group.anyRunning = anyRunning
            let item = DockerListItem.compose(group, children: children)
            if anyRunning {
                runningItems.append(item)
            } else {
                stoppedItems.append(item)
            }
        }

        // add ungrouped containers
        for container in ungroupedContainers {
            if container.running {
                runningItems.append(.container(container))
            } else {
                stoppedItems.append(.container(container))
            }
        }

        // sort by name within running/stopped sections
        // and within each section, sort by isGroup first
        runningItems.sort { a, b in
            if a.isGroup != b.isGroup {
                return a.isGroup
            }
            return a.containerName < b.containerName
        }
        stoppedItems.sort { a, b in
            if a.isGroup != b.isGroup {
                return a.isGroup
            }
            return a.containerName < b.containerName
        }

        // add running/stopped sections
        listItems += runningItems
        if showStopped && !stoppedItems.isEmpty {
            //listItems.append(DockerListItem(sectionLabel: "Stopped"))
            listItems += stoppedItems
        }

        return listItems
    }
}