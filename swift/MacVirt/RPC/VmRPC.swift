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
    var cpu: UInt
    var rosetta: Bool
    var networkProxy: String
    var networkBridge: Bool
    var mountHideShared: Bool
    var dataDir: String?
    var dockerSetContext: Bool
    var setupUseAdmin: Bool
    var k8sEnable: Bool
    var k8sExposeServices: Bool

    // camel case due to keyDecodingStrategy translating snake_case before it hits this
    // these are NOT the keys in the real json
    enum CodingKeys: String, CodingKey {
        case memoryMib = "memoryMib"
        case cpu = "cpu"
        case rosetta = "rosetta"
        case networkProxy = "networkProxy"
        case networkBridge = "networkBridge"
        case mountHideShared = "mountHideShared"
        case dataDir = "dataDir"
        case dockerSetContext = "docker.setContext"
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

class VmService: RPCService {
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

    func setConfig(_ config: VmConfig) async throws {
        try await invoke("SetConfig", params: config)
    }

    func resetConfig() async throws {
        try await invoke("ResetConfig")
    }

    func startSetup() async throws -> SetupInfo {
        try await invoke("StartSetup")
    }

    func isSshConfigWritable() async throws -> Bool {
        try await invoke("IsSshConfigWritable")
    }

    // MARK: - Docker
    func dockerContainerList() async throws -> [DKContainer] {
        try await invoke("DockerContainerList")
    }

    func dockerContainerStart(_ id: String) async throws {
        try await invoke("DockerContainerStart", params: IDRequest(id: id))
    }

    func dockerContainerStop(_ id: String) async throws {
        try await invoke("DockerContainerStop", params: IDRequest(id: id))
    }

    func dockerContainerKill(_ id: String) async throws {
        try await invoke("DockerContainerKill", params: IDRequest(id: id))
    }

    func dockerContainerRestart(_ id: String) async throws {
        try await invoke("DockerContainerRestart", params: IDRequest(id: id))
    }

    func dockerContainerPause(_ id: String) async throws {
        try await invoke("DockerContainerPause", params: IDRequest(id: id))
    }

    func dockerContainerUnpause(_ id: String) async throws {
        try await invoke("DockerContainerUnpause", params: IDRequest(id: id))
    }

    func dockerContainerRemove(_ id: String) async throws {
        try await invoke("DockerContainerDelete", params: IDRequest(id: id))
    }

    func dockerVolumeList() async throws -> DKVolumeListResponse {
        try await invoke("DockerVolumeList")
    }

    func dockerVolumeCreate(_ options: DKVolumeCreateOptions) async throws {
        try await invoke("DockerVolumeCreate", params: options)
    }

    func dockerVolumeRemove(_ id: String) async throws {
        try await invoke("DockerVolumeDelete", params: IDRequest(id: id))
    }

    func dockerImageList() async throws -> [DKImage] {
        try await invoke("DockerImageList")
    }

    func dockerImageRemove(_ id: String) async throws {
        try await invoke("DockerImageDelete", params: IDRequest(id: id))
    }

    func dockerSystemDf() async throws -> DKSystemDf {
        try await invoke("DockerSystemDf")
    }

    // MARK: - K8s
    func k8sPodDelete(namespace: String, name: String) async throws {
        try await invoke("K8sPodDelete", params: K8sNameRequest(namespace: namespace, name: name))
    }

    func k8sServiceDelete(namespace: String, name: String) async throws {
        try await invoke("K8sServiceDelete", params: K8sNameRequest(namespace: namespace, name: name))
    }

    func guiReportStarted() async throws {
        try await invoke("GuiReportStarted")
    }
}
