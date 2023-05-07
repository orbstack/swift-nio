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

enum DKVolumeAction {
    case remove
}

enum DKImageAction {
    case remove
}

enum MachineAction {
    case start
    case stop
    case restart
    case delete
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
    @Published var ongoingDockerVolumeActions: [String: DKVolumeAction] = [:]
    @Published var ongoingDockerImageActions: [String: DKImageAction] = [:]
    @Published var ongoingMachineActions: [String: MachineAction] = [:]

    func ongoingFor(_ cid: DockerContainerId) -> DKContainerAction? {
        ongoingDockerContainerActions[cid]
    }

    func ongoingForVolume(_ vid: String) -> DKVolumeAction? {
        ongoingDockerVolumeActions[vid]
    }

    func ongoingForImage(_ iid: String) -> DKImageAction? {
        ongoingDockerImageActions[iid]
    }

    func ongoingForMachine(_ mid: String) -> MachineAction? {
        ongoingMachineActions[mid]
    }

    func begin(_ cid: DockerContainerId, action: DKContainerAction) {
        ongoingDockerContainerActions[cid] = action
    }

    func beginVolume(_ vid: String, action: DKVolumeAction) {
        ongoingDockerVolumeActions[vid] = action
    }

    func beginImage(_ iid: String, action: DKImageAction) {
        ongoingDockerImageActions[iid] = action
    }

    func beginMachine(_ mid: String, action: MachineAction) {
        ongoingMachineActions[mid] = action
    }

    func end(_ cid: DockerContainerId) {
        ongoingDockerContainerActions[cid] = nil
    }

    func endVolume(_ vid: String) {
        ongoingDockerVolumeActions[vid] = nil
    }

    func endImage(_ iid: String) {
        ongoingDockerImageActions[iid] = nil
    }

    func endMachine(_ mid: String) {
        ongoingMachineActions[mid] = nil
    }
}
