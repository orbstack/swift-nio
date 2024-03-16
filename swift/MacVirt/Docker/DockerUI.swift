//
// Created by Danny Lin on 5/20/23.
//

import AppKit
import Defaults
import Foundation

extension DKContainer {
    func openInPlainTerminal() {
        Task {
            do {
                // prefer bash, otherwise use sh
                try await openTerminal(AppConfig.dockerExe, ["--context", "orbstack", "exec", "-it", id, "sh", "-c", "command -v bash > /dev/null && exec bash || exec sh"])
            } catch {
                NSLog("Open terminal failed: \(error)")
            }
        }
    }

    func openDebugShellFallback() {
        Task {
            do {
                try await openTerminal(AppConfig.ctlExe, ["debug", "--fallback", id])
            } catch {
                NSLog("Open terminal failed: \(error)")
            }
        }
    }

    func openDebugShell() {
        Task {
            do {
                try await openTerminal(AppConfig.ctlExe, ["debug", id])
            } catch {
                NSLog("Open terminal failed: \(error)")
            }
        }
    }

    func openFolder() {
        NSWorkspace.openFolder("\(Folders.nfsDockerContainers)/\(nameOrId)")
    }

    @MainActor
    func showLogs(windowTracker: WindowTracker) {
        if !windowTracker.openDockerLogWindowIds.contains(DockerContainerId.container(id: id)) {
            NSWorkspace.openSubwindow("docker/container-logs/\(id)")
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
        let protoSuffix = type == "tcp" ? "" : "  (\(type.uppercased()))"
        let portStr = ctrPort == localPort ? "\(ctrPort)" : "\(ctrPort) → \(localPort)"

        return "\(portStr)\(protoSuffix)"
    }

    var localPort: UInt16 {
        publicPort ?? privatePort
    }

    func openUrl() {
        let ctrPort = privatePort
        let localPort = publicPort ?? privatePort
        let httpProto = (ctrPort == 443 || ctrPort == 8443 || localPort == 443 || localPort == 8443) ? "https" : "http"
        if let url = URL(string: "\(httpProto)://localhost:\(localPort)") {
            NSWorkspace.shared.open(url)
        }
    }
}

extension DKMountPoint {
    var formatted: String {
        // don't show src - too long. dest matters more
        destination
    }

    func getOpenPath() -> String {
        if let volName = name,
           type == .volume
        {
            return "\(Folders.nfsDockerVolumes)/\(volName)"
        } else {
            return source
        }
    }

    func openSourceDirectory() {
        NSWorkspace.openFolder(getOpenPath())
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

enum WindowTitles {
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

    static let podLogsBase = "Pod Logs"
    static func podLogs(_ name: String?) -> String {
        if let name {
            return "\(name) — \(podLogsBase)"
        } else {
            return podLogsBase
        }
    }
}

struct DockerK8sGroup: Equatable {
    let anyRunning: Bool
}

enum DockerListItem: Identifiable, Equatable, AKListItem {
    case sectionLabel(String)
    case container(DKContainer)
    case compose(ComposeGroup, children: [DockerListItem])
    case k8sGroup(DockerK8sGroup, children: [DockerListItem])

    var id: DockerContainerId {
        switch self {
        case let .sectionLabel(label):
            return .sectionLabel(label)
        case let .container(container):
            return .container(id: container.id)
        case let .compose(group, _):
            return .compose(project: group.project)
        case .k8sGroup:
            return .k8sGroup
        }
    }

    var containerName: String {
        switch self {
        case let .container(container):
            return container.names.first ?? ""
        case let .compose(group, _):
            return group.project
        case .k8sGroup:
            return "Kubernetes"
        default:
            return ""
        }
    }

    var isGroup: Bool {
        switch self {
        case .compose, .k8sGroup:
            return true
        default:
            return false
        }
    }

    var listChildren: [any AKListItem]? {
        switch self {
        case let .compose(_, children):
            return children
        case let .k8sGroup(_, children):
            return children
        default:
            return nil
        }
    }

    var textLabel: String? {
        containerName
    }
}

enum DockerContainerLists {
    static func makeListItems(filteredContainers: [DKContainer]) -> (running: [DockerListItem], stopped: [DockerListItem]) {
        var runningItems: [DockerListItem] = []
        var stoppedItems: [DockerListItem] = []

        // collect compose groups and remove them from containers
        var ungroupedContainers: [DKContainer] = []
        var k8sContainers: [DKContainer] = []
        var composeGroups: [ComposeGroup: [DKContainer]] = [:]

        for container in filteredContainers {
            if let composeProject = container.composeProject {
                let group = ComposeGroup(project: composeProject)
                if composeGroups[group] == nil {
                    composeGroups[group] = [container]
                } else {
                    composeGroups[group]?.append(container)
                }
            } else if container.isK8s {
                k8sContainers.append(container)
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
            group.isFullCompose = containers.allSatisfy { $0.isFullCompose }
            let item = DockerListItem.compose(group, children: children)
            if anyRunning {
                runningItems.append(item)
            } else {
                stoppedItems.append(item)
            }
        }

        // add k8s items
        if !k8sContainers.isEmpty {
            let anyRunning = k8sContainers.contains(where: { $0.running })
            let children = k8sContainers.map { DockerListItem.container($0) }
            let group = DockerK8sGroup(anyRunning: anyRunning)
            let item = DockerListItem.k8sGroup(group, children: children)
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

        return (runningItems, stoppedItems)
    }
}
