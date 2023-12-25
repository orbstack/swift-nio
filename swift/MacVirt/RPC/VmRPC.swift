//
//  VmRPC.swift
//  MacVirt
//
//  Created by Danny Lin on 1/31/23.
//

import Foundation

struct VmConfig: Codable, Equatable {
    var memoryMib: UInt64
    var cpu: UInt
    var rosetta: Bool
    var networkProxy: String
    var networkBridge: Bool
    var networkHttps: Bool
    var mountHideShared: Bool
    var dataDir: String?
    var dockerSetContext: Bool
    var dockerNodeName: String
    var setupUseAdmin: Bool
    var k8sEnable: Bool
    var k8sExposeServices: Bool

    // camel case due to keyDecodingStrategy translating snake_case before it hits this
    // these are NOT the keys in the real json
    enum CodingKeys: String, CodingKey {
        case memoryMib
        case cpu
        case rosetta
        case networkProxy
        case networkBridge
        case networkHttps = "network.https"
        case mountHideShared
        case dataDir
        case dockerSetContext = "docker.setContext"
        case dockerNodeName = "docker.nodeName"
        case setupUseAdmin = "setup.useAdmin"
        case k8sEnable = "k8s.enable"
        case k8sExposeServices = "k8s.exposeServices"
    }
}

struct SetupInfo: Codable {
    var adminSymlinkCommands: [PHSymlinkRequest]?
    var adminMessage: String?
    var alertProfileChanged: String?
    var alertRequestAddPaths: [String]?
}

private struct K8sNameRequest: Codable {
    let namespace: String
    let name: String
}

private struct InternalUpdateTokenRequest: Codable {
    var refreshToken: String?
}

class VmService {
    private let c: JsonRPCClient

    init(client: JsonRPCClient) {
        c = client
    }

    func ping() async throws {
        try await c.call("Ping")
    }

    func stop() async throws {
        try await c.call("Stop")
        // TODO: handle EOF
    }

    func forceStop() async throws {
        try await c.call("ForceStop")
        // TODO: handle EOF
    }

    func resetData() async throws {
        try await c.call("ResetData")
    }

    func setConfig(_ config: VmConfig) async throws {
        try await c.call("SetConfig", args: config)
    }

    func resetConfig() async throws {
        try await c.call("ResetConfig")
    }

    func startSetup() async throws -> SetupInfo {
        try await c.call("StartSetup")
    }

    func isSshConfigWritable() async throws -> Bool {
        try await c.call("IsSshConfigWritable")
    }

    // MARK: - Docker

    func dockerContainerStart(_ id: String) async throws {
        try await c.call("DockerContainerStart", args: IDRequest(id: id))
    }

    func dockerContainerStop(_ id: String) async throws {
        try await c.call("DockerContainerStop", args: IDRequest(id: id))
    }

    func dockerContainerKill(_ id: String) async throws {
        try await c.call("DockerContainerKill", args: IDRequest(id: id))
    }

    func dockerContainerRestart(_ id: String) async throws {
        try await c.call("DockerContainerRestart", args: IDRequest(id: id))
    }

    func dockerContainerPause(_ id: String) async throws {
        try await c.call("DockerContainerPause", args: IDRequest(id: id))
    }

    func dockerContainerUnpause(_ id: String) async throws {
        try await c.call("DockerContainerUnpause", args: IDRequest(id: id))
    }

    func dockerContainerRemove(_ id: String) async throws {
        try await c.call("DockerContainerDelete", args: IDRequest(id: id))
    }

    func dockerVolumeCreate(_ options: DKVolumeCreateOptions) async throws {
        try await c.call("DockerVolumeCreate", args: options)
    }

    func dockerVolumeRemove(_ id: String) async throws {
        try await c.call("DockerVolumeDelete", args: IDRequest(id: id))
    }

    func dockerImageRemove(_ id: String) async throws {
        try await c.call("DockerImageDelete", args: IDRequest(id: id))
    }

    // MARK: - K8s

    func k8sPodDelete(namespace: String, name: String) async throws {
        try await c.call("K8sPodDelete", args: K8sNameRequest(namespace: namespace, name: name))
    }

    func k8sServiceDelete(namespace: String, name: String) async throws {
        try await c.call("K8sServiceDelete", args: K8sNameRequest(namespace: namespace, name: name))
    }

    func guiReportStarted() async throws {
        try await c.call("GuiReportStarted")
    }

    func internalUpdateToken(_ token: String?) async throws {
        try await c.call("InternalUpdateToken", args: InternalUpdateTokenRequest(refreshToken: token))
    }

    func internalRefreshDrm() async throws {
        try await c.call("InternalRefreshDrm")
    }
}
