//
//  VmRPC.swift
//  MacVirt
//
//  Created by Danny Lin on 1/31/23.
//

import Foundation
import SwiftJSONRPC

struct VmConfig: Codable, Equatable {
    var memoryMib: UInt64
}

struct SetupInfo: Codable {
    var adminShellCommand: String?
    var adminMessage: String?
    var alertProfileChanged: String?
    var alertRequestAddPaths: [String]?
}

struct DockerContainer: Codable, Identifiable {
    var id: String
    var names: [String]
    var image: String
    var imageID: String
    var command: String
    var created: Int64
    //var ports: [Ports]
    var sizeRw: Int64?
    var sizeRootFs: Int64?
    var labels: [String: String]
    var state: String
    var status: String
    //var hostConfig: HostConfig
    //var networkSettings: SummaryNetworkSettings
    //var mounts: [MountPoint]

    enum CodingKeys: String, CodingKey {
        case id = "Id"
        case names = "Names"
        case image = "Image"
        case imageID = "ImageID"
        case command = "Command"
        case created = "Created"
        //case ports = "Ports"
        case sizeRw = "SizeRw"
        case sizeRootFs = "SizeRootFs"
        case labels = "Labels"
        case state = "State"
        case status = "Status"
        //case hostConfig = "HostConfig"
        //case networkSettings = "NetworkSettings"
        //case mounts = "Mounts"
    }
}

class VmService: RPCService {
    static let shared = SconService(client: RPCClient(url: URL(string: "http://127.0.0.1:62420")!))

    func ping() async throws {
        try await invoke("Ping")
    }

    func stop() async throws {
        try await invoke("Stop")
        // TODO handle EOF
    }

    func forceStop() async throws {
        try await invoke("ForceStop")
        // TODO handle EOF
    }

    func resetData() async throws {
        try await invoke("ResetData")
    }

    func getConfig() async throws -> VmConfig {
        try await invoke("GetConfig")
    }

    func patchConfig(_ config: VmConfig) async throws {
        try await invoke("PatchConfig", params: config)
    }

    func resetConfig() async throws {
        try await invoke("ResetConfig")
    }

    func startSetup() async throws -> SetupInfo {
        try await invoke("StartSetup")
    }

    func finishSetup() async throws {
        try await invoke("FinishSetup")
    }

    func listDockerContainers() async throws -> [DockerContainer] {
        try await invoke("ListDockerContainers")
    }
}
