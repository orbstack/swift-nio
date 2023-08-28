//
// Created by Danny Lin on 8/28/23.
//

import Foundation
import AppKit

struct SectionGroup<Element: Identifiable>: Identifiable {
    let title: String
    let items: [Element]

    var id: String {
        title
    }
}

struct K8SResourceLists {
    static func groupItems<Resource: K8SResource>(_ resources: [Resource],
                                                  showSystemNs: Bool = false) -> [SectionGroup<Resource>] {
        let grouped = Dictionary(grouping: resources, by: { $0.namespace })
        return grouped
            .map { SectionGroup(title: $0.key,
                    // sort items within section
                    items: $0.value.sorted { $0.name < $1.name }) }
            .filter { showSystemNs || $0.title != "kube-system" }
            // sort sections
            .sorted { $0.title < $1.title }
    }
}

extension K8SPod {
    @MainActor
    func showLogs(vmModel: VmViewModel) {
        if !vmModel.openK8sLogWindowIds.contains(id) {
            let b64URL = "\(namespace)/\(name)".data(using: .utf8)!.base64URLEncodedString()
            NSWorkspace.shared.open(URL(string: "orbstack://k8s/pod-logs/\(b64URL)")!)
        } else {
            // find window by title and bring to front
            for window in NSApp.windows {
                if window.title == WindowTitles.podLogs(name) {
                    window.makeKeyAndOrderFront(nil)
                    break
                }
            }
        }
    }

    func openInTerminal() {
        Task {
            do {
                try await openTerminal(AppConfig.kubectlExe, ["exec", "--context", K8sConstants.context, "-it", "-n", namespace, "pod/\(name)", "--", "sh"])
            } catch {
                NSLog("Open terminal failed: \(error)")
            }
        }
    }
}