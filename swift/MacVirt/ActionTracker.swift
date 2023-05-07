//
// Created by Danny Lin on 5/7/23.
//

import Foundation

enum DKContainerAction {
    case start
    case stop
    case pause
    case unpause
    case restart
    case remove

    var isStartStop: Bool {
        switch self {
        case .start, .stop, .restart:
            return true
        default:
            return false
        }
    }
}

enum DockerContainerId: Hashable {
    case container(id: String)
    // ID by config files + working dir to prevent duplicate project name from breaking stuff
    case compose(project: String, configFiles: String)
    case notDocker(key: String)
}

@MainActor
class ActionTracker: ObservableObject {
    // also includes compose (same ID type)
    @Published var ongoingDockerContainerActions: [DockerContainerId: DKContainerAction] = [:]

    func ongoingFor(_ cid: DockerContainerId) -> DKContainerAction? {
        ongoingDockerContainerActions[cid]
    }

    func begin(_ cid: DockerContainerId, action: DKContainerAction) {
        ongoingDockerContainerActions[cid] = action
    }

    func end(_ cid: DockerContainerId) {
        ongoingDockerContainerActions[cid] = nil
    }
}
