//
// Created by Danny Lin on 5/20/23.
//

import Foundation
import AppKit

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
            NSWorkspace.shared.open(URL(string: "orbstack://docker/containers/logs/\(id)")!)
        } else {
            // find window by title and bring to front
            for window in NSApp.windows {
                if window.title == "Logs: \(userName)" {
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