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
    case delete

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
    case importing
    case delete
}

enum DKImageAction {
    case delete
    case exporting
}

enum DKNetworkAction {
    case delete
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
    case sectionLabel(String)
    case k8sGroup
}

enum K8SResourceAction {
    case delete

    var userDesc: String {
        switch self {
        case .delete:
            return "delete"
        }
    }
}

@MainActor
class ActionTracker: ObservableObject {
    // also includes compose (same ID type)
    @Published var ongoingDockerContainerActions: [DockerContainerId: DKContainerAction] = [:]
    @Published var ongoingDockerVolumeActions: [String: DKVolumeAction] = [:]
    @Published var ongoingDockerImageActions: [String: DKImageAction] = [:]
    @Published var ongoingDockerNetworkActions: [String: DKNetworkAction] = [:]
    @Published var ongoingMachineActions: [String: MachineAction] = [:]
    @Published var ongoingK8sActions: [K8SResourceId: K8SResourceAction] = [:]

    @Published var ongoingMachineExports: Set<String> = []
    @Published var ongoingVolumeExports: Set<String> = []
    @Published var ongoingImageImports: Set<String> = []

    func ongoingFor(_ cid: DockerContainerId) -> DKContainerAction? {
        ongoingDockerContainerActions[cid]
    }

    func ongoingFor(volume: DKVolume) -> DKVolumeAction? {
        ongoingDockerVolumeActions[volume.id]
    }

    func ongoingFor(image: DKSummaryAndFullImage) -> DKImageAction? {
        ongoingDockerImageActions[image.id]
    }

    func ongoingFor(network: DKNetwork) -> DKNetworkAction? {
        ongoingDockerNetworkActions[network.id]
    }

    func ongoingFor(machine: ContainerRecord) -> MachineAction? {
        ongoingMachineActions[machine.id]
    }

    func ongoingFor(_ k8s: K8SResourceId) -> K8SResourceAction? {
        ongoingK8sActions[k8s]
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

    func beginNetwork(_ nid: String, action: DKNetworkAction) {
        ongoingDockerNetworkActions[nid] = action
    }

    func beginMachine(_ mid: String, action: MachineAction) {
        ongoingMachineActions[mid] = action
    }

    func begin(_ k8s: K8SResourceId, action: K8SResourceAction) {
        ongoingK8sActions[k8s] = action
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

    func endNetwork(_ nid: String) {
        ongoingDockerNetworkActions[nid] = nil
    }

    func endMachine(_ mid: String) {
        ongoingMachineActions[mid] = nil
    }

    func end(_ k8s: K8SResourceId) {
        ongoingK8sActions[k8s] = nil
    }

    func with(cid: DockerContainerId, action: DKContainerAction, _ block: () throws -> Void)
        rethrows
    {
        begin(cid, action: action)
        defer { end(cid) }
        try block()
    }

    func with(cid: DockerContainerId, action: DKContainerAction, _ block: () async throws -> Void)
        async rethrows
    {
        begin(cid, action: action)
        defer { end(cid) }
        try await block()
    }

    func with(volumeId: String, action: DKVolumeAction, _ block: () throws -> Void) rethrows {
        beginVolume(volumeId, action: action)
        defer { endVolume(volumeId) }
        try block()
    }

    func with(volumeId: String, action: DKVolumeAction, _ block: () async throws -> Void)
        async rethrows
    {
        beginVolume(volumeId, action: action)
        defer { endVolume(volumeId) }
        try await block()
    }

    func with(imageId: String, action: DKImageAction, _ block: () throws -> Void) rethrows {
        beginImage(imageId, action: action)
        defer { endImage(imageId) }
        try block()
    }

    func with(imageId: String, action: DKImageAction, _ block: () async throws -> Void)
        async rethrows
    {
        beginImage(imageId, action: action)
        defer { endImage(imageId) }
        try await block()
    }

    func with(networkId: String, action: DKNetworkAction, _ block: () throws -> Void) rethrows {
        beginNetwork(networkId, action: action)
        defer { endNetwork(networkId) }
        try block()
    }

    func with(networkId: String, action: DKNetworkAction, _ block: () async throws -> Void)
        async rethrows
    {
        beginNetwork(networkId, action: action)
        defer { endNetwork(networkId) }
        try await block()
    }

    func with(machine: ContainerRecord, action: MachineAction, _ block: () throws -> Void) rethrows
    {
        beginMachine(machine.id, action: action)
        defer { endMachine(machine.id) }
        try block()
    }

    func with(machine: ContainerRecord, action: MachineAction, _ block: () async throws -> Void)
        async rethrows
    {
        beginMachine(machine.id, action: action)
        defer { endMachine(machine.id) }
        try await block()
    }

    func with(k8s: K8SResourceId, action: K8SResourceAction, _ block: () throws -> Void) rethrows {
        begin(k8s, action: action)
        defer { end(k8s) }
        try block()
    }

    func with(k8s: K8SResourceId, action: K8SResourceAction, _ block: () async throws -> Void)
        async rethrows
    {
        begin(k8s, action: action)
        defer { end(k8s) }
        try await block()
    }

    func withMachineExport(id: String, _ block: () async throws -> Void) async rethrows {
        ongoingMachineExports.insert(id)
        ongoingMachineExports = ongoingMachineExports
        defer {
            ongoingMachineExports.remove(id)
            ongoingMachineExports = ongoingMachineExports
        }

        try await block()
    }

    func withVolumeExport(id: String, _ block: () async throws -> Void) async rethrows {
        ongoingVolumeExports.insert(id)
        ongoingVolumeExports = ongoingVolumeExports
        defer {
            ongoingVolumeExports.remove(id)
            ongoingVolumeExports = ongoingVolumeExports
        }

        try await block()
    }

    func withImageImport(id: String, _ block: () async throws -> Void) async rethrows {
        if ongoingImageImports.contains(id) {
            return
        }

        ongoingImageImports.insert(id)
        ongoingImageImports = ongoingImageImports
        defer {
            ongoingImageImports.remove(id)
            ongoingImageImports = ongoingImageImports
        }

        try await block()
    }
}
