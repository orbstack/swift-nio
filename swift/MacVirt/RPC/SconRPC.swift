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
}

struct ImageSpec: Codable, Equatable {
    var distro: String
    var version: String
    var arch: String
    var variant: String
}

struct ContainerRecord: AKListItem, Codable, Identifiable, Equatable {
    var id: String
    var name: String
    var image: ImageSpec

    var config: MachineConfig

    var builtin: Bool
    var state: ContainerState

    var running: Bool {
        state == .running || state == .stopping
    }

    var textLabel: String? {
        name
    }
}

struct MachineConfig: Codable, Equatable {
    var isolated: Bool
}

struct CreateRequest: Codable {
    var name: String
    var image: ImageSpec
    var userPassword: String?
    var cloudInitUserData: String?
}

private struct GetByIDRequest: Codable {
    var id: String
}

private struct GetByNameRequest: Codable {
    var name: String
}

private struct ContainerRenameRequest: Codable {
    var container: ContainerRecord
    var newName: String
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

    func listContainers() async throws -> [ContainerRecord] {
        return try await c.call("ListContainers")
    }

    func getById(_ id: String) async throws -> ContainerRecord {
        return try await c.call("GetByID", args: GetByIDRequest(id: id))
    }

    func getByName(_ name: String) async throws -> ContainerRecord {
        return try await c.call("GetByName", args: GetByNameRequest(name: name))
    }

    func getDefaultContainer() async throws -> ContainerRecord {
        return try await c.call("GetDefaultContainer")
    }

    func setDefaultContainer(_ record: ContainerRecord) async throws {
        try await c.call("SetDefaultContainer", args: record)
    }

    func clearDefaultContainer() async throws {
        try await c.call("ClearDefaultContainer")
    }

    func containerStart(_ record: ContainerRecord) async throws {
        try await c.call("ContainerStart", args: record)
    }

    func containerStop(_ record: ContainerRecord) async throws {
        try await c.call("ContainerStop", args: record)
    }

    func containerRestart(_ record: ContainerRecord) async throws {
        try await c.call("ContainerRestart", args: record)
    }

    func containerDelete(_ record: ContainerRecord) async throws {
        try await c.call("ContainerDelete", args: record)
    }

    func containerRename(_ record: ContainerRecord, newName: String) async throws {
        try await c.call("ContainerRename", args: ContainerRenameRequest(container: record, newName: newName))
    }

    func internalDockerFastDf() async throws -> DKSystemDf {
        try await c.call("InternalDockerFastDf")
    }

    func internalDeleteK8s() async throws {
        try await c.call("InternalDeleteK8s")
    }

    func internalGuiReportStarted() async throws {
        try await c.call("InternalGuiReportStarted")
    }
}
