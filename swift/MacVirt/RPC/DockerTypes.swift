//
// Created by Danny Lin on 3/19/23.
//

import Defaults
import Foundation

private let relativeDateFormatter = RelativeDateTimeFormatter()
private let iso8601TzDateFormatter = {
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withFullDate, .withTime, .withFractionalSeconds, .withTimeZone]
    return formatter
}()
private let nowTimeThreshold: TimeInterval = 5  // sec

struct IDRequest: Codable {
    let id: String
}

struct DKContainer: Codable, Identifiable, Hashable {
    var id: String
    var names: [String]
    var image: String
    var imageId: String
    var command: String
    var created: Int64
    var ports: [DKPort]
    var sizeRw: Int64?
    var sizeRootFs: Int64?
    var labels: [String: String]?
    var state: String
    var status: String
    // var hostConfig: HostConfig
    var networkSettings: DKSummaryNetworkSettings?
    var mounts: [DKMountPoint]

    var running: Bool {
        state == "running"
    }

    var paused: Bool {
        state == "paused"
    }

    // docker counts restarting as running for /containers/json list inclusion
    var runningOrRestarting: Bool {
        switch state {
        case "running", "restarting":
            return true
        default:
            return false
        }
    }

    var statusDot: StatusDot {
        if status.contains("(unhealthy)") {
            return .orange
        } else if state == "paused" {
            return .gray
        } else if running {
            return .green
        } else {
            return .red
        }
    }

    var composeProject: String? {
        labels?[DockerLabels.composeProject]
    }

    var composeService: String? {
        // for oneoff `docker compose run`, hide service name to avoid duplicate/conflict with the service it's based on (for both name and domain)
        // scon uses the same logic for domains
        composeOneoff ? nil : labels?[DockerLabels.composeService]
    }

    private var composeOneoff: Bool {
        labels?[DockerLabels.composeOneoff] == "True"
    }

    var composeNumber: String? {
        labels?[DockerLabels.composeNumber]
    }

    var composeConfigFiles: [String]? {
        labels?[DockerLabels.composeConfigFiles]?.split(separator: ",").map { String($0) }
    }

    var isRealCompose: Bool {
        composeProject != nil && composeService != nil
    }

    var isK8s: Bool {
        labels?[DockerLabels.k8sType] != nil
    }

    var k8sNamespace: String? {
        labels?[DockerLabels.k8sNamespace]
    }

    var k8sPodName: String? {
        labels?[DockerLabels.k8sPodName]
    }

    var k8sContainerName: String? {
        labels?[DockerLabels.k8sContainerName]
    }

    var userName: String {
        // prefer compose service label first (because we'll be grouped if it's compose)
        if let k8sName = labels?[DockerLabels.k8sContainerName],
            let k8sPodName = labels?[DockerLabels.k8sPodName]
        {
            return "\(k8sPodName) (\(k8sName))"
        } else if let composeService {
            // all containers have numbers, even w/o scale
            if let composeNumber, composeNumber != "1" {
                // for --scale
                return "\(composeService) (\(composeNumber))"
            } else {
                return composeService
            }
        } else {
            return names
                .lazy
                .map { $0.deletingPrefix("/") }
                .joined(separator: ", ")
        }
    }

    var nameOrId: String {
        names.first?.deletingPrefix("/") ?? id
    }

    var ipAddresses: [String] {
        networkSettings?.networks.values
            .lazy
            .map { $0.ipAddress }
            .filter { !$0.isEmpty }
            ?? []
    }

    var ipAddress: String? {
        ipAddresses.first
    }

    @MainActor
    func getPreferredProto(_ model: VmViewModel) -> String {
        // "!= false" because default is true
        (model.config?.networkHttps != false) ? "https" : "http"
    }

    // use same logic as scon server
    // 1. first custom domain
    // 2. service.project.orb.local (compose)
    // 3. container name.orb.local
    var preferredDomain: String? {
        // containers without IPs can't have a domain
        // pods don't have a docker-level netns
        guard !ipAddresses.isEmpty else {
            return nil
        }

        if let label = labels?[DockerLabels.customDomains],
            let _domain = label.split(separator: ",").first
        {
            // remove wildcard
            let domain = String(_domain)
            // make it RFC 1035 compliant, or Tomcat complains
            // we have aliases for _ -> - if it was a default name (compose or container name)
            // _ is allowed for custom names
            return String(domain.deletingPrefix("*."))
                .replacingOccurrences(of: "_", with: "-")
        } else if let project = composeProject,
            let service = composeService
        {
            var optNum = ""
            if let composeNumber, composeNumber != "1" {
                // for --scale
                optNum = "-\(composeNumber)"
            }

            // make it RFC 1035 compliant, or Tomcat complains
            return "\(service)\(optNum).\(project).orb.local"
                .replacingOccurrences(of: "_", with: "-")
        } else {
            return "\(userName).orb.local"
        }
    }

    var cid: DockerContainerId {
        .container(id: id)
    }

    enum CodingKeys: String, CodingKey {
        case id = "Id"
        case names = "Names"
        case image = "Image"
        case imageId = "ImageID"
        case command = "Command"
        case created = "Created"
        case ports = "Ports"
        case sizeRw = "SizeRw"
        case sizeRootFs = "SizeRootFs"
        case labels = "Labels"
        case state = "State"
        case status = "Status"
        // case hostConfig = "HostConfig"
        case networkSettings = "NetworkSettings"
        case mounts = "Mounts"
    }
}

struct DKSummaryNetworkSettings: Codable, Identifiable, Hashable {
    var networks: [String: DKNetworkEndpointSettings]

    var id: String {
        networks.keys.sorted().joined(separator: ", ")
    }

    enum CodingKeys: String, CodingKey {
        case networks = "Networks"
    }
}

struct DKNetworkEndpointSettings: Codable, Identifiable, Hashable {
    // var ipamConfig: DKEndpointIPAMConfig?
    var links: [String]?
    var aliases: [String]?
    // Operational data
    var networkId: String
    var endpointId: String
    var gateway: String
    var ipAddress: String
    var ipPrefixLen: Int
    var ipv6Gateway: String
    var globalIPv6Address: String
    var globalIPv6PrefixLen: Int
    var macAddress: String
    var driverOpts: [String: String]?

    var id: String {
        networkId
    }

    enum CodingKeys: String, CodingKey {
        // case ipamConfig = "IPAMConfig"
        case links = "Links"
        case aliases = "Aliases"
        case networkId = "NetworkID"
        case endpointId = "EndpointID"
        case gateway = "Gateway"
        case ipAddress = "IPAddress"
        case ipPrefixLen = "IPPrefixLen"
        case ipv6Gateway = "IPv6Gateway"
        case globalIPv6Address = "GlobalIPv6Address"
        case globalIPv6PrefixLen = "GlobalIPv6PrefixLen"
        case macAddress = "MacAddress"
        case driverOpts = "DriverOpts"
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
    case bind
    case volume
    case tmpfs
    case npipe
    case cluster
}

enum DKMountPropagation: String, Codable, Hashable {
    case rprivate
    case privat = "private"
    case rshared
    case shared
    case rslave
    case slave
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
    // let clusterVolumeSpec: ClusterVolumeSpec?

    enum CodingKeys: String, CodingKey {
        case name = "Name"
        case labels = "Labels"
        case driver = "Driver"
        case driverOpts = "DriverOpts"
        // case clusterVolumeSpec = "ClusterVolumeSpec"
    }
}

struct DKVolume: AKListItem, Codable, Identifiable, Equatable {
    let createdAt: Date?
    let driver: String
    let labels: [String: String]?
    let mountpoint: String
    let name: String
    let options: [String: String]?
    let scope: String
    // let status: [String: any Codable]?
    let usageData: DKVolumeUsageData?

    var id: String {
        name
    }

    var formattedCreatedAt: String {
        guard let createdAt else { return "unknown" }

        // fix "in 0 seconds"
        if Date().timeIntervalSince(createdAt) < nowTimeThreshold {
            return "just now"
        }

        return relativeDateFormatter.localizedString(for: createdAt, relativeTo: Date())
    }

    var textLabel: String? {
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
        // case status = "Status"
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

struct DKNetwork: Codable, Equatable, Identifiable, AKListItem {
    let name: String
    // wtf, this is omitempty in Go?
    let id: String
    let created: String?
    let scope: String?
    let driver: String
    let enableIPv6: Bool
    let ipam: DKIPAM?

    let `internal`: Bool
    let attachable: Bool
    let ingress: Bool
    let configFrom: DKConfigReference
    let configOnly: Bool
    let containers: [String: DKContainerEndpoint]?
    let options: [String: String]
    let labels: [String: String]

    // on create only
    let checkDuplicate: Bool?

    var textLabel: String? {
        name
    }

    // TODO: don't keep re-parsing this
    var createdDate: Date? {
        guard let created else { return nil }
        return iso8601TzDateFormatter.date(from: created)
    }

    var formattedCreated: String {
        guard let date = createdDate else { return "unknown" }

        // fix "in 0 seconds"
        if Date().timeIntervalSince(date) < nowTimeThreshold {
            return "just now"
        }

        return relativeDateFormatter.localizedString(for: date, relativeTo: Date())
    }

    enum CodingKeys: String, CodingKey {
        case name = "Name"
        case id = "Id"
        case created = "Created"
        case scope = "Scope"
        case driver = "Driver"
        case enableIPv6 = "EnableIPv6"
        case ipam = "IPAM"

        case `internal` = "Internal"
        case attachable = "Attachable"
        case ingress = "Ingress"
        case configFrom = "ConfigFrom"
        case configOnly = "ConfigOnly"
        case containers = "Containers"
        case options = "Options"
        case labels = "Labels"

        case checkDuplicate = "CheckDuplicate"
    }
}

struct DKIPAM: Codable, Equatable {
    let driver: String
    let config: [DKIPAMConfig]?
    let options: [String: String]?

    enum CodingKeys: String, CodingKey {
        case driver = "Driver"
        case config = "Config"
        case options = "Options"
    }
}

struct DKIPAMConfig: Codable, Equatable {
    let subnet: String
    let gateway: String

    enum CodingKeys: String, CodingKey {
        case subnet = "Subnet"
        case gateway = "Gateway"
    }
}

struct DKConfigReference: Codable, Equatable {
    let network: String

    enum CodingKeys: String, CodingKey {
        case network = "Network"
    }
}

struct DKContainerEndpoint: Codable, Equatable {
    let name: String
    let endpointId: String
    let macAddress: String
    let ipv4Address: String
    let ipv6Address: String

    enum CodingKeys: String, CodingKey {
        case name = "Name"
        case endpointId = "EndpointID"
        case macAddress = "MacAddress"
        case ipv4Address = "IPv4Address"
        case ipv6Address = "IPv6Address"
    }
}

struct DKImage: Codable, Equatable, Identifiable {
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

    var _userTag: String {
        if let tag = repoTags?.first, tag != "<none>:<none>" {
            return tag
        } else if let repoDigest = repoDigests?.first,
            let repoName = repoDigest.split(separator: "@").first
        {
            // recover part of the tag from images that were pulled from a repo
            return "\(repoName) (\(userId.prefix(12).description))"
        } else {
            return userId.prefix(12).description
        }
    }

    var userTag: String {
        // containerd image store returns these; old docker didn't
        _userTag
            .replacingOccurrences(of: "docker.io/library/", with: "")
            .replacingOccurrences(of: "docker.io/", with: "")
    }

    var formattedSize: String {
        ByteCountFormatter.string(fromByteCount: size, countStyle: .file)
    }

    var formattedCreated: String {
        let date = Date(timeIntervalSince1970: TimeInterval(created))
        // fix "in 0 seconds"
        if Date().timeIntervalSince(date) < nowTimeThreshold {
            return "just now"
        }
        // fix "0001-01-01T00:00:00Z" (-62135596800): https://github.com/orbstack/orbstack/issues/1254
        if created <= 0 {
            return "unknown"
        }

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

struct DKFullImage: Codable, Equatable {
    let architecture: String

    enum CodingKeys: String, CodingKey {
        case architecture = "Architecture"
    }
}

struct DKSummaryAndFullImage: AKListItem, Codable, Equatable, Identifiable {
    var id: String { summary.id }
    let summary: DKImage
    let full: DKFullImage

    var textLabel: String? {
        summary.userTag
    }

    enum CodingKeys: String, CodingKey {
        case summary = "Summary"
        case full = "Full"
    }
}

struct DKSystemDf: Codable {
    let layersSize: Int64
    let images: [DKImage]?
    // layers
    // containers, etc
    let volumes: [DKVolume]

    enum CodingKeys: String, CodingKey {
        case layersSize = "LayersSize"
        case images = "Images"
        case volumes = "Volumes"
    }
}

struct DKNetworkCreateResponse: Decodable {
    let id: String
    let warning: String?

    enum CodingKeys: String, CodingKey {
        case id = "Id"
        case warning = "Warning"
    }
}

struct DKAPIError: Decodable {
    let message: String
}

enum DockerLabels {
    static let composeProject = "com.docker.compose.project"
    static let composeService = "com.docker.compose.service"
    static let composeOneoff = "com.docker.compose.oneoff"
    static let composeConfigFiles = "com.docker.compose.project.config_files"
    static let composeWorkingDir = "com.docker.compose.project.working_dir"
    // for --scale
    static let composeNumber = "com.docker.compose.container-number"

    static let k8sType = "io.kubernetes.docker.type"
    static let k8sPodName = "io.kubernetes.pod.name"
    static let k8sContainerName = "io.kubernetes.container.name"
    static let k8sNamespace = "io.kubernetes.pod.namespace"

    static let customDomains = "dev.orbstack.domains"
}
