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
        if !vmModel.openLogWindowIds.contains(id) {
            NSWorkspace.shared.open(URL(string: "orbstack://docker/container-logs/\(id)")!)
        } else {
            // find window by title and bring to front
            for window in NSApp.windows {
                if window.title == "\(WindowTitles.logs): \(userName)" {
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
}

struct WindowTitles {
    static let projectLogs = "Project Logs"
    static let logs = "Logs" // also for empty
}

struct DockerListItem: Identifiable, Equatable {
    var builtinRecord: ContainerRecord? = nil
    var sectionLabel: String? = nil
    var container: DKContainer? = nil
    var composeGroup: ComposeGroup? = nil
    var children: [DockerListItem]? = nil

    var id: DockerContainerId {
        if let builtinRecord {
            return .notDocker(key: "BUI:\(builtinRecord.id)")
        }
        if let sectionLabel {
            return .notDocker(key: "SEC:\(sectionLabel)")
        }
        if let container {
            return .container(id: container.id)
        }
        if let composeGroup {
            return .compose(project: composeGroup.project, configFiles: composeGroup.configFiles)
        }
        return .notDocker(key: "")
    }

    var containerName: String {
        container?.names.first ?? composeGroup?.project ?? ""
    }

    var isGroup: Bool {
        composeGroup != nil
    }

    init(builtinRecord: ContainerRecord) {
        self.builtinRecord = builtinRecord
    }

    init(sectionLabel: String) {
        self.sectionLabel = sectionLabel
    }

    init(container: DKContainer) {
        self.container = container
    }

    init(composeGroup: ComposeGroup, children: [DockerListItem]) {
        self.composeGroup = composeGroup
        self.children = children
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
            if let composeProject = container.labels[DockerLabels.composeProject],
               let configFiles = container.labels[DockerLabels.composeConfigFiles] {
                let group = ComposeGroup(project: composeProject, configFiles: configFiles)
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
        for (group, containers) in composeGroups {
            let children = containers.map { DockerListItem(container: $0) }
            var item = DockerListItem(composeGroup: group, children: children)
            // if ANY container in the group is running, show the group as running
            if containers.contains(where: { $0.running }) {
                item.composeGroup?.anyRunning = true
                runningItems.append(item)
            } else {
                stoppedItems.append(item)
            }
        }

        // add ungrouped containers
        for container in ungroupedContainers {
            if container.running {
                runningItems.append(DockerListItem(container: container))
            } else {
                stoppedItems.append(DockerListItem(container: container))
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