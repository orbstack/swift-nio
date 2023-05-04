//
// Created by Danny Lin on 3/19/23.
//

import Foundation

private let relativeDateFormatter = RelativeDateTimeFormatter()

struct IDRequest: Codable {
    let id: String
}

struct DKContainer: Codable, Identifiable, Hashable {
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
    let usageData: DKVolumeUsageData?

    var id: String {
        name
    }

    var formattedCreatedAt: String {
        // ISO 8601
        let date = ISO8601DateFormatter().date(from: createdAt ?? "") ?? Date()
        return relativeDateFormatter.localizedString(for: date, relativeTo: Date())
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
        case usageData = "UsageData"
    }
}

struct DKVolumeUsageData: Codable, Equatable {
    let refCount: Int
    let size: Int64

    enum CodingKeys: String, CodingKey {
        case refCount = "RefCount"
        case size = "Size"
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

struct DKImage: Codable, Identifiable {
    let id: String
    let containers: Int
    let created: Int64
    let labels: [String: String]?
    let parentId: String?
    let repoDigests: [String]?
    let repoTags: [String]?
    let sharedSize: Int64
    let size: Int64
    let virtualSize: Int64

    var tag: String {
        repoTags?.first ?? "<none>"
    }

    var userId: String {
        id.replacingOccurrences(of: "sha256:", with: "")
    }

    var hasTag: Bool {
        if let tag = repoTags?.first, tag != "<none>:<none>" {
            return true
        } else {
            return false
        }
    }

    var userTag: String {
        if let tag = repoTags?.first, tag != "<none>:<none>" {
            return tag
        } else {
            return userId.prefix(12).description
        }
    }

    var formattedSize: String {
        ByteCountFormatter.string(fromByteCount: size, countStyle: .file)
    }

    var formattedCreated: String {
        let date = Date(timeIntervalSince1970: TimeInterval(created))
        return relativeDateFormatter.localizedString(for: date, relativeTo: Date())
    }

    enum CodingKeys: String, CodingKey {
        case id = "Id"
        case containers = "Containers"
        case created = "Created"
        case labels = "Labels"
        case parentId = "ParentId"
        case repoDigests = "RepoDigests"
        case repoTags = "RepoTags"
        case sharedSize = "SharedSize"
        case size = "Size"
        case virtualSize = "VirtualSize"
    }
}

struct DKSystemDf: Codable {
    let layersSize: Int64
    let images: [DKImage]
    //layers
    //containers, etc
    let volumes: [DKVolume]

    enum CodingKeys: String, CodingKey {
        case layersSize = "LayersSize"
        case images = "Images"
        case volumes = "Volumes"
    }
}