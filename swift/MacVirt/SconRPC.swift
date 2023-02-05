//
//  SconRPC.swift
//  MacVirt
//
//  Created by Danny Lin on 1/31/23.
//

import Foundation
import SwiftJSONRPC

struct ImageSpec: Codable {
    var distro: String
    var version: String
    var arch: String
    var variant: String
}

struct ContainerRecord: Codable, Identifiable {
    var id: String
    var name: String
    var image: ImageSpec
    var isolated: Bool
    
    var builtin: Bool
    var running: Bool
    var deleting: Bool
}

fileprivate struct CreateRequest: Codable {
    var name: String
    var image: ImageSpec
    var userPassword: String?
}

fileprivate struct GetByIDRequest: Codable {
    var id: String
}

fileprivate struct GetByNameRequest: Codable {
    var name: String
}

class SconService: RPCService {
    static let shared = SconService(client: RPCClient(url: URL(string: "http://127.0.0.1:62421")!))

    func ping() async throws {
        try await invoke("Ping")
    }

    @discardableResult
    func create(name: String, image: ImageSpec, userPassword: String?) async throws -> ContainerRecord {
        return try await invoke("Create", params: CreateRequest(
            name: name,
            image: image,
            userPassword: userPassword
        ))
    }
    
    func listContainers() async throws -> [ContainerRecord] {
        return try await invoke("ListContainers")
    }
    
    func getById(_ id: String) async throws -> ContainerRecord {
        return try await invoke("GetByID", params: GetByIDRequest(id: id))
    }
    
    func getByName(_ name: String) async throws -> ContainerRecord {
        return try await invoke("GetByName", params: GetByNameRequest(name: name))
    }
    
    func getDefaultContainer() async throws -> ContainerRecord {
        return try await invoke("GetDefaultContainer")
    }

    func containerStart(_ record: ContainerRecord) async throws {
        try await invoke("ContainerStart", params: record)
    }

    func containerStop(_ record: ContainerRecord) async throws {
        try await invoke("ContainerStop", params: record)
    }

    func containerDelete(_ record: ContainerRecord) async throws {
        try await invoke("ContainerDelete", params: record)
    }

    func containerFreeze(_ record: ContainerRecord) async throws {
        try await invoke("ContainerFreeze", params: record)
    }

    func containerUnfreeze(_ record: ContainerRecord) async throws {
        try await invoke("ContainerUnfreeze", params: record)
    }

    func stopServerVm() async throws {
        try await invoke("StopServerVM")
    }
}
