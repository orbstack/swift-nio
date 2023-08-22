//
// Created by Danny Lin on 5/7/23.
//

import Foundation

enum DKContainerAction {
    case start
    case stop
    case kill
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
    // ID by project only, or we can break with multiple config files
    case compose(project: String)

    // not docker
    case builtinRecord(String)
    case sectionLabel(String)
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

    func ongoingFor(volume: DKVolume) -> DKVolumeAction? {
        ongoingDockerVolumeActions[volume.id]
    }

    func ongoingFor(image: DKImage) -> DKImageAction? {
        ongoingDockerImageActions[image.id]
    }

    func ongoingFor(machine: ContainerRecord) -> MachineAction? {
        ongoingMachineActions[machine.id]
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

    func with(cid: DockerContainerId, action: DKContainerAction, _ block: () throws -> Void) rethrows {
        begin(cid, action: action)
        defer { end(cid) }
        try block()
    }

    func with(cid: DockerContainerId, action: DKContainerAction, _ block: () async throws -> Void) async rethrows {
        begin(cid, action: action)
        defer { end(cid) }
        try await block()
    }

    func with(volumeId: String, action: DKVolumeAction, _ block: () throws -> Void) rethrows {
        beginVolume(volumeId, action: action)
        defer { endVolume(volumeId) }
        try block()
    }

    func with(volumeId: String, action: DKVolumeAction, _ block: () async throws -> Void) async rethrows {
        beginVolume(volumeId, action: action)
        defer { endVolume(volumeId) }
        try await block()
    }

    func with(imageId: String, action: DKImageAction, _ block: () throws -> Void) rethrows {
        beginImage(imageId, action: action)
        defer { endImage(imageId) }
        try block()
    }

    func with(imageId: String, action: DKImageAction, _ block: () async throws -> Void) async rethrows {
        beginImage(imageId, action: action)
        defer { endImage(imageId) }
        try await block()
    }

    func with(machine: ContainerRecord, action: MachineAction, _ block: () throws -> Void) rethrows {
        beginMachine(machine.id, action: action)
        defer { endMachine(machine.id) }
        try block()
    }

    func with(machine: ContainerRecord, action: MachineAction, _ block: () async throws -> Void) async rethrows {
        beginMachine(machine.id, action: action)
        defer { endMachine(machine.id) }
        try await block()
    }
}
