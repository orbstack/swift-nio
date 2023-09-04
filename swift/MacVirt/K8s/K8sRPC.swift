//
// Created by Danny Lin on 8/28/23.
//

import Foundation

private let relativeDateFormatter = makeRelativeDateFormatter()

private func makeRelativeDateFormatter() -> RelativeDateTimeFormatter {
    let formatter = RelativeDateTimeFormatter()
    formatter.formattingContext = .standalone
    return formatter
}

enum K8SResourceId: Equatable, Identifiable, Hashable {
    // uid isn't very useful. it breaks using ID as a check
    // (node, namespace, name) is unique
    // TODO maybe we do want uid, but exclude it from Identifiable and Hashable for matching
    case pod(namespace: String, name: String)
    case deployment(namespace: String, name: String)
    case statefulSet(namespace: String, name: String)
    case daemonSet(namespace: String, name: String)
    case job(namespace: String, name: String)
    case replicaSet(namespace: String, name: String)
    case service(namespace: String, name: String)

    static func podFromNamespaceAndName(_ namespaceAndName: String) -> K8SResourceId? {
        let parts = namespaceAndName.split(separator: "/", maxSplits: 1)
        if parts.count == 2 {
            return .pod(namespace: String(parts[0]), name: String(parts[1]))
        }
        return nil
    }

    var id: String {
        "\(namespace)/\(name)"
    }

    // TODO there's gotta be a better way to do this
    var name: String {
        switch self {
        case .pod(_, let name):
            return name
        case .deployment(_, let name):
            return name
        case .statefulSet(_, let name):
            return name
        case .daemonSet(_, let name):
            return name
        case .job(_, let name):
            return name
        case .replicaSet(_, let name):
            return name
        case .service(_, let name):
            return name
        }
    }

    var namespace: String {
        switch self {
        case .pod(let namespace, _):
            return namespace
        case .deployment(let namespace, _):
            return namespace
        case .statefulSet(let namespace, _):
            return namespace
        case .daemonSet(let namespace, _):
            return namespace
        case .job(let namespace, _):
            return namespace
        case .replicaSet(let namespace, _):
            return namespace
        case .service(let namespace, _):
            return namespace
        }
    }

    var typeDesc: String {
        switch self {
        case .pod:
            return "pod"
        case .deployment:
            return "deployment"
        case .statefulSet:
            return "statefulSet"
        case .daemonSet:
            return "daemonSet"
        case .job:
            return "job"
        case .replicaSet:
            return "replicaSet"
        case .service:
            return "service"
        }
    }
}

protocol K8SResource: AKListItem {
    var id: K8SResourceId { get }
    var name: String { get }
    var namespace: String { get }
}

struct K8SPod: K8SResource, Codable, Equatable, Hashable {
    let metadata: K8SPodMetadata
    let spec: K8SPodSpec
    let status: K8SPodStatus

    var statusStr: String {
        // if any container is failed, the pod is failed
        if let containerStatuses = status.containerStatuses {
            for containerStatus in containerStatuses {
                if let state = containerStatus.state {
                    if let waiting = state.waiting {
                        return waiting.reason ?? "Waiting"
                    } else if let terminated = state.terminated {
                        return terminated.reason ?? "Terminated"
                    }
                }
            }
        }

        return status.phase
    }

    var id: K8SResourceId {
        .pod(namespace: namespace, name: name)
    }

    var name: String {
        metadata.name
    }

    var namespace: String {
        metadata.namespace
    }

    var preferredDomain: String {
        // TODO domains
        //"\(name).\(namespace).svc.cluster.local"
        status.podIP ?? "localhost"
    }

    var restartCount: Int {
        status.containerStatuses?.reduce(0) { $0 + ($1.restartCount ?? 0) } ?? 0
    }

    var ageStr: String {
        relativeDateFormatter.localizedString(for: metadata.creationTimestamp, relativeTo: Date())
            // TODO do this better
            .replacingOccurrences(of: " ago", with: "")
            .replacingOccurrences(of: "in ", with: "")
    }
}

struct K8SPodMetadata: Codable, Equatable, Hashable {
    // TODO what's optional?
    let name: String
    let namespace: String
    let uid: String
    let creationTimestamp: Date
    let labels: [String: String]?
    let annotations: [String: String]?
    let ownerReferences: [K8SPodOwnerReference]?
}

struct K8SPodOwnerReference: Codable, Equatable, Hashable {
    // TODO what's optional?
    let apiVersion: String?
    let kind: String?
    let name: String?
    let uid: String?
    let controller: Bool?
    let blockOwnerDeletion: Bool?
}

struct K8SPodSpec: Codable, Equatable, Hashable {
    // TODO what's optional?
    let serviceAccount: String?
    let serviceAccountName: String?
    let nodeName: String?
    let priority: Int?
    let tolerations: [K8SPodToleration]?
}

struct K8SPodToleration: Codable, Equatable, Hashable {
    // TODO what's optional?
    let key: String?
    let op: String?
    let effect: String?
    let tolerationSeconds: Int?

    enum CodingKeys: String, CodingKey {
        case key
        case op = "operator"
        case effect
        case tolerationSeconds
    }
}

struct K8SPodStatus: Codable, Equatable, Hashable {
    let phase: String
    let hostIP: String?
    let podIP: String?
    let podIPs: [K8SPodIP]? // v6
    let startTime: Date?
    let containerStatuses: [K8SContainerStatus]?
    let qosClass: String?
}

struct K8SContainerStatus: Codable, Equatable, Hashable, Identifiable {
    // TODO what's optional?
    let name: String?
    // I think this is polymorphic
    let state: K8SContainerState?
    let lastState: [String: K8SContainerStateDetails]?
    let ready: Bool?
    let restartCount: Int?
    let image: String?
    let imageID: String?
    let containerID: String?
    let started: Bool?

    var id: String {
        name ?? UUID().uuidString
    }
}

struct K8SContainerState: Codable, Equatable, Hashable {
    let running: K8SContainerStateDetails?
    let terminated: K8SContainerStateDetails?
    let waiting: K8SContainerStateDetails?
}

struct K8SContainerStateDetails: Codable, Equatable, Hashable {
    // these ARE optional
    let startedAt: Date?
    let exitCode: Int?
    let reason: String?
    let finishedAt: Date?
    let containerID: String?
}

struct K8SPodIP: Codable, Equatable, Hashable {
    let ip: String
}

struct K8SService: K8SResource, Codable, Equatable, Hashable {
    let metadata: K8SServiceMetadata
    let spec: K8SServiceSpec
    let status: K8SServiceStatus

    var id: K8SResourceId {
        .service(namespace: namespace, name: name)
    }

    var name: String {
        metadata.name
    }

    var namespace: String {
        metadata.namespace
    }

    var systemImage: String {
        switch spec.type {
        case .loadBalancer:
            return "network"
        case .clusterIP:
            return "point.3.filled.connected.trianglepath.dotted"
        case .externalName:
            return "network"
        case .nodePort:
            return "rectangle.connected.to.line.below"
        }
    }

    var canOpen: Bool {
        spec.type != .externalName && isWebService
    }

    var hasLocalhost: Bool {
        spec.type == .loadBalancer || spec.type == .nodePort
    }

    var preferredDomain: String {
        // TODO domains
        //"\(name).\(namespace).svc.cluster.local"
        switch spec.type {
        case .loadBalancer:
            return "k8s.orb.local"
        case .clusterIP:
            return spec.clusterIP ?? "localhost"
        case .externalName:
            return "localhost"
        case .nodePort:
            return "localhost"
        }
    }

    var preferredDomainAndPort: String {
        let firstPort = spec.ports?.first
        guard let firstPort else { return preferredDomain }
        return "\(preferredDomain):\(firstPort.port)"
    }

    func wrapURL(host: String) -> String? {
        let tcpPort = spec.ports?.first(where: { $0.match(proto: "TCP", port: 443, name: "https") })
            ?? spec.ports?.first(where: { $0.match(proto: "TCP", port: 80, name: "http") })
        guard let tcpPort else { return nil }

        // loadBalancer ignores nodeport
        let port = spec.type == .loadBalancer ? tcpPort.port : (tcpPort.nodePort ?? tcpPort.port)
        let scheme = (tcpPort.appProtocol == "https" || port == 443) ? "https" : "http"
        if (scheme == "https" && port == 443) || (scheme == "http" && port == 80) {
            return "\(scheme)://\(preferredDomain)"
        } else {
            return "\(scheme)://\(preferredDomain):\(port)"
        }
    }

    func wrapURLNoScheme(host: String) -> String? {
        let pre = wrapURL(host: host)?
            .replacingOccurrences(of: "http://", with: "")
            .replacingOccurrences(of: "https://", with: "")
        guard let pre else { return nil }

        // special case: loadBalancer = *. if no port
        if spec.type == .loadBalancer && !pre.contains(":") {
            return "*.\(pre)"
        }

        return pre
    }

    var isWebService: Bool {
        spec.ports?.contains { port in
            port.proto == "TCP" &&
                    (port.match(proto: "TCP", port: 443, name: "https") ||
                    port.match(proto: "TCP", port: 80, name: "http"))
        } ?? false
    }

    var externalIP: String? {
        status.loadBalancer?.ingress?.first?.ip
    }

    var ageStr: String {
        relativeDateFormatter.localizedString(for: metadata.creationTimestamp, relativeTo: Date())
        // TODO do this better
        .replacingOccurrences(of: " ago", with: "")
        .replacingOccurrences(of: "in ", with: "")
    }
}

struct K8SServiceMetadata: Codable, Equatable, Hashable {
    // TODO what's optional?
    let name: String
    let namespace: String
    let uid: String
    let creationTimestamp: Date
    let labels: [String: String]?
    let annotations: [String: String]?
    let finalizers: [String]?
}

struct K8SServiceSpec: Codable, Equatable, Hashable {
    // TODO what's optional?
    let clusterIP: String?
    let clusterIPs: [String]?
    let ipFamilies: [String]?
    let ipFamilyPolicy: String?
    let ports: [K8SServicePort]?
    let selector: [String: String]?
    let sessionAffinity: String?
    let type: K8SServiceType
}

enum K8SServiceType: String, Codable, Equatable, Hashable {
    case clusterIP = "ClusterIP"
    case externalName = "ExternalName"
    case loadBalancer = "LoadBalancer"
    case nodePort = "NodePort"
}

struct K8SServicePort: Codable, Equatable, Hashable, Identifiable {
    // TODO what's optional?
    let name: String?
    let proto: String?
    let port: Int
    //let targetPort: IntOrString? // can be string
    let nodePort: Int?
    let appProtocol: String?

    var id: String {
        "\(proto ?? "")-\(port)"
    }

    func match(proto: String, port: Int, name: String) -> Bool {
        self.proto == proto &&
                (self.port == port || self.nodePort == port ||
                        self.appProtocol == name || self.name == name)
    }

    enum CodingKeys: String, CodingKey {
        case name
        case proto = "protocol"
        case port
        //case targetPort
        case nodePort
        case appProtocol
    }
}

struct K8SServiceStatus: Codable, Equatable, Hashable {
    // TODO what's optional?
    // polymorphic?
    let loadBalancer: K8SServiceLoadBalancer?
}

struct K8SServiceLoadBalancer: Codable, Equatable, Hashable {
    let ingress: [K8SServiceLoadBalancerIngress]?
}

struct K8SServiceLoadBalancerIngress: Codable, Equatable, Hashable {
    // TODO what's optional?
    let ip: String?
}