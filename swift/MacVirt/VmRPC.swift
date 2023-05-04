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
}

struct VmConfigPatch: Codable, Equatable {
    var memoryMib: UInt64? = nil
    var cpu: UInt? = nil
    var rosetta: Bool? = nil
    var networkProxy: String? = nil
}

struct SetupInfo: Codable {
    var adminShellCommand: String?
    var adminMessage: String?
    var alertProfileChanged: String?
    var alertRequestAddPaths: [String]?
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

    func patchConfig(_ config: VmConfigPatch) async throws {
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
        try await invoke("DockerContainerRemove", params: IDRequest(id: id))
    }

    func dockerVolumeList() async throws -> DKVolumeListResponse {
        try await invoke("DockerVolumeList")
    }

    func dockerVolumeCreate(_ options: DKVolumeCreateOptions) async throws {
        try await invoke("DockerVolumeCreate", params: options)
    }

    func dockerVolumeRemove(_ id: String) async throws {
        try await invoke("DockerVolumeRemove", params: IDRequest(id: id))
    }

    func dockerImageList() async throws -> [DKImage] {
        try await invoke("DockerImageList")
    }

    func dockerImageRemove(_ id: String) async throws {
        try await invoke("DockerImageRemove", params: IDRequest(id: id))
    }
}
