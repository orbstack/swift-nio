//
//  SconRPC.swift
//  MacVirt
//
//  Created by Danny Lin on 1/31/23.
//

import Foundation

enum ContainerState: String, Codable {
    case creating
    case starting
    case running
    case stopping
    case stopped
    case deleting
    case provisioning

    var friendlyName: String {
        switch self {
        case .creating:
            return "Creating"
        case .starting:
            return "Starting"
        case .running:
            return "Running"
        case .stopping:
            return "Stopping"
        case .stopped:
            return "Stopped"
        case .deleting:
            return "Deleting"
        case .provisioning:
            return "Provisioning"
        }
    }

    var isInitializing: Bool {
        return self == .creating || self == .provisioning
    }
}

struct ImageSpec: Codable, Equatable {
    var distro: String
    var version: String
    var arch: String
    var variant: String
}

struct ContainerRecord: Codable, Identifiable, Equatable {
    var id: String
    var name: String
    var image: ImageSpec

    var config: MachineConfig

    var builtin: Bool
    var state: ContainerState

    var running: Bool {
        state == .running || state == .stopping
    }
}

struct ContainerInfo: AKListItem, Codable, Identifiable, Equatable {
    var record: ContainerRecord
    var diskSize: UInt64?

    var id: String {
        record.id
    }

    var textLabel: String? {
        record.name
    }
}

struct MachineConfig: Codable, Equatable {
    var isolated: Bool
    var defaultUsername: String?
}

struct CreateRequest: Codable {
    var name: String
    var image: ImageSpec
    var config: MachineConfig
    var userPassword: String?
    var cloudInitUserData: String?
}

private struct GenericContainerRequest: Codable {
    var key: String
}

private struct ContainerRenameRequest: Codable {
    var containerKey: String
    var newName: String
}

private struct ContainerCloneRequest: Codable {
    var containerKey: String
    var newName: String
}

private struct ContainerExportRequest: Codable {
    var containerKey: String
    var hostPath: String
}

struct ImportContainerFromHostPathRequest: Codable {
    var newName: String?
    var hostPath: String
}

enum StatsID: Codable, Equatable, Hashable, Comparable {
    // cgroupPath > pid
    case cgroupPath(String)
    case pid(UInt32)
}

struct GetStatsRequest: Codable {
    var includeProcessCgPaths: [String]
}

struct StatsResponse: Codable {
    var entries: [StatsEntry]
}

enum StatsEntity: Codable, Equatable {
    case machine(id: String)
    case container(id: String)
    case service(id: String)
}

struct StatsEntry: Codable {
    var id: StatsID
    var entity: StatsEntity

    var cpuUsageUsec: UInt64
    var diskReadBytes: UInt64
    var diskWriteBytes: UInt64

    var memoryBytes: UInt64

    var children: [StatsEntry]?
}

struct InternalDockerExportVolumeToHostPathRequest: Codable {
    var volumeId: String
    var hostPath: String
}

struct InternalDockerImportVolumeFromHostPathRequest: Codable {
    var newName: String
    var hostPath: String
}

class SconService {
    private let c: JsonRPCClient

    init(client: JsonRPCClient) {
        c = client
    }

    func ping() async throws {
        try await c.call("Ping")
    }

    @discardableResult
    func create(_ req: CreateRequest) async throws -> ContainerRecord {
        return try await c.call("Create", args: req)
    }

    @discardableResult
    func importContainerFromHostPath(_ req: ImportContainerFromHostPathRequest) async throws
        -> ContainerRecord
    {
        return try await c.call("ImportContainerFromHostPath", args: req)
    }

    func listContainers() async throws -> [ContainerInfo] {
        return try await c.call("ListContainers")
    }

    func getByKey(_ key: String) async throws -> ContainerInfo {
        return try await c.call("GetByKey", args: GenericContainerRequest(key: key))
    }

    func getDefaultContainer() async throws -> ContainerRecord {
        return try await c.call("GetDefaultContainer")
    }

    func setDefaultContainer(_ key: String) async throws {
        try await c.call("SetDefaultContainer", args: GenericContainerRequest(key: key))
    }

    func clearDefaultContainer() async throws {
        try await c.call("ClearDefaultContainer")
    }

    func containerStart(_ key: String) async throws {
        try await c.call("ContainerStart", args: GenericContainerRequest(key: key))
    }

    func containerStop(_ key: String) async throws {
        try await c.call("ContainerStop", args: GenericContainerRequest(key: key))
    }

    func containerRestart(_ key: String) async throws {
        try await c.call("ContainerRestart", args: GenericContainerRequest(key: key))
    }

    func containerDelete(_ key: String) async throws {
        try await c.call("ContainerDelete", args: GenericContainerRequest(key: key))
    }

    func containerRename(_ key: String, newName: String) async throws {
        try await c.call(
            "ContainerRename", args: ContainerRenameRequest(containerKey: key, newName: newName))
    }

    func containerExport(_ key: String, hostPath: String) async throws {
        return try await c.call(
            "ContainerExportToHostPath",
            args: ContainerExportRequest(containerKey: key, hostPath: hostPath))
    }

    func containerClone(_ key: String, newName: String) async throws {
        try await c.call(
            "ContainerClone", args: ContainerCloneRequest(containerKey: key, newName: newName))
    }

    func getStats(_ req: GetStatsRequest) async throws -> StatsResponse {
        return try await c.call("GetStats", args: req)
    }

    func internalDockerFastDf() async throws -> DKSystemDf {
        try await c.call("InternalDockerFastDf")
    }

    func internalDockerImportVolumeFromHostPath(
        _ req: InternalDockerImportVolumeFromHostPathRequest
    ) async throws {
        try await c.call("InternalDockerImportVolumeFromHostPath", args: req)
    }

    func internalDockerExportVolumeToHostPath(_ req: InternalDockerExportVolumeToHostPathRequest)
        async throws
    {
        try await c.call("InternalDockerExportVolumeToHostPath", args: req)
    }

    func internalDeleteK8s() async throws {
        try await c.call("InternalDeleteK8s")
    }

    func internalGuiReportStarted() async throws {
        try await c.call("InternalGuiReportStarted")
    }
}
