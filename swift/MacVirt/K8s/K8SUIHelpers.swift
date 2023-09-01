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
            .lazy
            .map { SectionGroup(title: $0.key,
                    items: $0.value
                        .lazy
                        // k8s API service is always there. consider it system so we can show empty state
                        .filter { showSystemNs || ($0.id != K8sConstants.apiResId) }
                        // sort items within section
                        .sorted { $0.name < $1.name }) }
            // remove empty groups caused by filtering above
            .filter { !$0.items.isEmpty && (showSystemNs || ($0.title != "kube-system")) }
            // sort sections
            .sorted { $0.title < $1.title }
    }
}

extension K8SPod {
    @MainActor
    func showLogs(vmModel: VmViewModel) {
        if !vmModel.openK8sLogWindowIds.contains(id) {
            let b64URL = "\(namespace)/\(name)".data(using: .utf8)!.base64URLEncodedString()
            if let url = URL(string: "orbstack://k8s/pod-logs/\(b64URL)") {
                NSWorkspace.shared.open(url)
            }
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

    var uiState: K8SPodUIState {
        switch statusStr {
        case "Running":
            return .running
        case "PodInitializing", "ContainerCreating", "Pending", "Terminating":
            return .loading
        case "Completed":
            return .completed
        case "Error", "ImagePullBackOff", "CrashLoopBackOff", "CreateContainerConfigError", "InvalidImageName":
            return .error
        default:
            // neutral state
            return .completed
        }
    }
}

enum K8SPodUIState {
    case loading
    case running
    case error
    case completed
}

extension K8SServiceType {
    var uiColor: NSColor {
        switch self {
        case .clusterIP:
            return .systemBlue
        case .nodePort:
            return .systemGreen
        case .loadBalancer:
            return .systemOrange
        case .externalName:
            return .systemPurple
        }
    }
}