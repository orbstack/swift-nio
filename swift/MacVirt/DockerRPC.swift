//
// Created by Danny Lin on 3/19/23.
//

import Foundation

struct IDRequest: Codable {
    let id: String
}

struct DKContainer: Codable, Identifiable {
    var id: String
    var names: [String]
    var image: String
    var imageID: String
    var command: String
    var created: Int64
    var ports: [DKPort]
    var sizeRw: Int64?
    var sizeRootFs: Int64?
    var labels: [String: String]
    var state: String
    var status: String
    //var hostConfig: HostConfig
    //var networkSettings: SummaryNetworkSettings
    var mounts: [DKMountPoint]

    var running: Bool {
        state == "running"
    }

    enum CodingKeys: String, CodingKey {
        case id = "Id"
        case names = "Names"
        case image = "Image"
        case imageID = "ImageID"
        case command = "Command"
        case created = "Created"
        case ports = "Ports"
        case sizeRw = "SizeRw"
        case sizeRootFs = "SizeRootFs"
        case labels = "Labels"
        case state = "State"
        case status = "Status"
        //case hostConfig = "HostConfig"
        //case networkSettings = "NetworkSettings"
        case mounts = "Mounts"
    }
}

struct DKPort: Codable, Identifiable, Hashable {
    let ip: String?
    let privatePort: UInt16
    let publicPort: UInt16?
    let type: String

    var id: String {
        "\(ip ?? "nil")\(privatePort)\(publicPort ?? 0)\(type)"
    }

    var publicPortInt: UInt16 {
        publicPort ?? privatePort
    }

    enum CodingKeys: String, CodingKey {
        case ip = "IP"
        case privatePort = "PrivatePort"
        case publicPort = "PublicPort"
        case type = "Type"
    }
}

enum DKMountType: String, Codable, Hashable {
    case bind = "bind"
    case volume = "volume"
    case tmpfs = "tmpfs"
    case npipe = "npipe"
    case cluster = "cluster"
}

enum DKMountPropagation: String, Codable, Hashable {
    case rprivate = "rprivate"
    case privat = "private"
    case rshared = "rshared"
    case shared = "shared"
    case rslave = "rslave"
    case slave = "slave"
}

struct DKMountPoint: Codable, Identifiable, Hashable {
    let type: DKMountType?
    let name: String?
    let source: String
    let destination: String
    let driver: String?
    let mode: String
    let rw: Bool
    let propagation: DKMountPropagation?

    var id: String {
        "\(type?.rawValue ?? "nil")\(name ?? "nil")\(source)\(destination)\(driver ?? "nil")\(mode)\(rw)\(propagation?.rawValue ?? "nil")"
    }

    enum CodingKeys: String, CodingKey {
        case type = "Type"
        case name = "Name"
        case source = "Source"
        case destination = "Destination"
        case driver = "Driver"
        case mode = "Mode"
        case rw = "RW"
        case propagation = "Propagation"
    }
}

struct DKVolumeCreateOptions: Codable {
    let name: String?
    let labels: [String: String]?
    let driver: String?
    let driverOpts: [String: String]?
    //let clusterVolumeSpec: ClusterVolumeSpec?

    enum CodingKeys: String, CodingKey {
        case name = "Name"
        case labels = "Labels"
        case driver = "Driver"
        case driverOpts = "DriverOpts"
        //case clusterVolumeSpec = "ClusterVolumeSpec"
    }
}

struct DKVolume: Codable, Identifiable, Equatable {
    let createdAt: String?
    let driver: String
    let labels: [String: String]?
    let mountpoint: String
    let name: String
    let options: [String: String]?
    let scope: String
    //let status: [String: any Codable]?
    //let usageData: VolumeUsageData?

    var id: String {
        name
    }

    enum CodingKeys: String, CodingKey {
        case createdAt = "CreatedAt"
        case driver = "Driver"
        case labels = "Labels"
        case mountpoint = "Mountpoint"
        case name = "Name"
        case options = "Options"
        case scope = "Scope"
        //case status = "Status"
        //case usageData = "UsageData"
    }
}

struct DKVolumeListResponse: Codable {
    let volumes: [DKVolume]
    let warnings: [String]?

    enum CodingKeys: String, CodingKey {
        case volumes = "Volumes"
        case warnings = "Warnings"
    }
}
