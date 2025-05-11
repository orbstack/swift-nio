//
//  ActivityMonitorRootView.swift
//  MacVirt
//
//  Created by Danny Lin on 4/7/25.
//

import Defaults
import SwiftUI

private struct ActivityMonitorItem: AKListItem, Equatable, Identifiable {
    let entity: ActivityMonitorEntity
    var id: ActivityMonitorID { entity.id }

    var cpuPercent: Float?
    var memoryBytes: UInt64
    var diskRwBytes: UInt64?

    var children: [ActivityMonitorItem]?

    var listChildren: [any AKListItem]? { children }
    var textLabel: String? { entity.description }

    static func synthetic(entity: ActivityMonitorEntity, children: [ActivityMonitorItem])
        -> ActivityMonitorItem
    {
        // manual reduce loop to deal with nil reduction
        var cpuPercent: Float? = nil
        var memoryBytes: UInt64 = 0
        var diskRwBytes: UInt64? = nil
        for item in children {
            if let newCpuPercent = item.cpuPercent {
                cpuPercent = cpuPercent.map { $0 + newCpuPercent } ?? newCpuPercent
            }
            memoryBytes += item.memoryBytes
            if let newDiskRwBytes = item.diskRwBytes {
                diskRwBytes = diskRwBytes.map { $0 + newDiskRwBytes } ?? newDiskRwBytes
            }
        }

        return ActivityMonitorItem(
            entity: entity, cpuPercent: cpuPercent, memoryBytes: memoryBytes,
            diskRwBytes: diskRwBytes, children: children)
    }
}

private enum ActivityMonitorID: Equatable, Hashable, Comparable {
    case machine(id: String)
    case container(id: String)
    case compose(project: String)

    // synthetics
    case k8sGroup
    case k8sNamespace(String)
    case k8sServices
    case dockerEngine
    case buildkit
}

private enum ActivityMonitorEntity: Identifiable, Comparable, CustomStringConvertible {
    case machine(record: ContainerRecord)
    case container(container: DKContainer)
    case compose(project: String)

    // synthetics
    case k8sGroup
    case k8sNamespace(String)
    case k8sServices
    case dockerEngine
    case buildkit

    var id: ActivityMonitorID {
        switch self {
        case .machine(let record):
            return .machine(id: record.id)
        case .container(let container):
            return .container(id: container.id)
        case .compose(let project):
            return .compose(project: project)
        case .k8sGroup:
            return .k8sGroup
        case .k8sNamespace(let ns):
            return .k8sNamespace(ns)
        case .k8sServices:
            return .k8sServices
        case .dockerEngine:
            return .dockerEngine
        case .buildkit:
            return .buildkit
        }
    }

    var description: String {
        switch self {
        case .machine(let record):
            return record.name
        case .container(let container):
            return container.userName
        case .compose(let project):
            return project
        case .k8sGroup:
            return "Kubernetes"
        case .k8sNamespace(let ns):
            return ns
        case .k8sServices:
            return "Services"
        case .dockerEngine:
            return "Engine"
        case .buildkit:
            return "Builds"
        }
    }

    static func < (lhs: ActivityMonitorEntity, rhs: ActivityMonitorEntity) -> Bool {
        return lhs.description < rhs.description
    }
}

private let initialRefreshInterval = 0.5  // seconds
private let refreshInterval = 1.5  // seconds

private let nsecPerSec = 1e9

private enum Columns {
    static let name = "name"
    static let cpuPercent = "cpuPercent"
    static let memoryBytes = "memoryBytes"
    static let diskRwBytes = "diskRwBytes"
}

struct ActivityMonitorRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    private let timer = Timer.publish(every: refreshInterval, on: .main, in: .common).autoconnect()

    @StateObject private var model = ActivityMonitorViewModel()
    @State private var selection: Set<ActivityMonitorID> = []
    @State private var sort = AKSortDescriptor(columnId: Columns.cpuPercent, ascending: false)

    var body: some View {
        StateWrapperView {
            AKList(
                AKSection.single(model.items), selection: $selection, sort: $sort, rowHeight: 24,
                flat: false, expandByDefault: true,
                autosaveName: Defaults.Keys.activityMonitor_autosaveOutline,
                columns: [
                    akColumn(id: Columns.name, title: "Name", width: 200, alignment: .left) {
                        item in
                        HStack {
                            switch item.entity {
                            case .machine(let record):
                                if record.id == ContainerIds.docker {
                                    Image(systemName: "shippingbox")
                                        .resizable()
                                        .aspectRatio(contentMode: .fit)
                                        .frame(width: 16, height: 16)
                                    Text("Containers")
                                } else {
                                    Image("distro_\(record.image.distro)")
                                        .resizable()
                                        .aspectRatio(contentMode: .fit)
                                        .frame(width: 16, height: 16)
                                    Text(item.entity.description)
                                }

                            case .container(let container):
                                DockerContainerImage(container: container)
                                    .scaleEffect(0.5)  // 32px -> 16px
                                    .frame(width: 16, height: 16)
                                Text(item.entity.description)

                            case .compose(let project):
                                DockerComposeGroupImage(project: project)
                                    .scaleEffect(0.5)  // 32px -> 16px
                                    .frame(width: 16, height: 16)
                                Text(item.entity.description)

                            case .k8sGroup:
                                K8sIcon()
                                    .scaleEffect(0.5)  // 32px -> 16px
                                    .frame(width: 16, height: 16)
                                Text(item.entity.description)

                            case .k8sNamespace:
                                Text(item.entity.description)

                            case .k8sServices:
                                Text(item.entity.description)

                            case .dockerEngine:
                                Text(item.entity.description)

                            case .buildkit:
                                Text(item.entity.description)
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .lineLimit(1)
                    },
                    akColumn(id: Columns.cpuPercent, title: "CPU %", width: 75, alignment: .right) {
                        item in
                        if let cpuPercent = item.cpuPercent {
                            Text(cpuPercent.formatted(.number.precision(.fractionLength(1))))
                                .frame(maxWidth: .infinity, alignment: .trailing)
                        }
                    },
                    akColumn(id: Columns.memoryBytes, title: "Memory", width: 75, alignment: .right)
                    { item in
                        Text(
                            ByteCountFormatter.string(
                                fromByteCount: Int64(item.memoryBytes), countStyle: .memory)
                        )
                        .frame(maxWidth: .infinity, alignment: .trailing)
                    },
                    akColumn(
                        id: Columns.diskRwBytes, title: "Disk I/O", width: 75, alignment: .right
                    ) {
                        item in
                        if let diskRwBytes = item.diskRwBytes {
                            if diskRwBytes > 0 {
                                let formatted = ByteCountFormatter.string(
                                    fromByteCount: Int64(diskRwBytes), countStyle: .memory)
                                Text("\(formatted)/s")
                                    .frame(maxWidth: .infinity, alignment: .trailing)
                            } else {
                                Text("0 b/s")
                                    .frame(maxWidth: .infinity, alignment: .trailing)
                            }
                        }
                    },
                ]
            )
            .onAppear {
                Task { @MainActor in
                    await model.refresh(vmModel: vmModel, desc: sort)

                    // populate delta-based stats faster at the beginning
                    do {
                        try await Task.sleep(
                            nanoseconds: UInt64(initialRefreshInterval * nsecPerSec))
                        await model.refresh(vmModel: vmModel, desc: sort)
                    } catch {
                        // ignore
                    }
                }
            }
            .onChange(of: sort) { newSort in
                model.reSort(desc: newSort)
            }
            .onReceive(timer) { _ in
                Task { @MainActor in
                    await model.refresh(vmModel: vmModel, desc: sort)
                }
            }
        }
        .navigationTitle("Activity Monitor")
    }
}

private struct StatsResult {
    let entries: [StatsID: StatsEntry]
    let time: ContinuousClock.Instant
}

@MainActor
private class ActivityMonitorViewModel: ObservableObject {
    private var lastStats: StatsResult? = nil

    @Published var items: [ActivityMonitorItem] = []

    func refresh(vmModel: VmViewModel, desc: AKSortDescriptor) async {
        var newStats: StatsResponse!
        do {
            newStats = try await vmModel.tryGetStats(GetStatsRequest(includeProcessCgPaths: []))
        } catch {
            return
        }
        let newEntries = Dictionary(
            uniqueKeysWithValues: newStats.entries.makeIterator().map { ($0.id, $0) })

        let now = ContinuousClock.now

        var newRootItems = [ActivityMonitorItem]()
        var newDockerItems = [String?: [ActivityMonitorItem]]()
        var newK8sItems = [String: [ActivityMonitorItem]]()
        var dockerMachineItem: ActivityMonitorItem?
        var dockerEngineItem: ActivityMonitorItem?
        var buildkitItem: ActivityMonitorItem?
        var k8sServicesItem: ActivityMonitorItem?
        for entry in newEntries.values {
            let item = entryToItem(entry: entry, vmModel: vmModel, now: now)
            guard let item else { continue }

            switch item.entity {
            case .machine(let record):
                if record.id == ContainerIds.docker {
                    dockerMachineItem = item
                } else {
                    newRootItems.append(item)
                }
            case .container(let container):
                if let k8sNs = container.k8sNamespace {
                    newK8sItems[k8sNs, default: []].append(item)
                } else {
                    newDockerItems[container.composeProject, default: []].append(item)
                }
            case .dockerEngine:
                dockerEngineItem = item
            case .buildkit:
                buildkitItem = item
            case .k8sServices:
                k8sServicesItem = item
            default:
                fatalError("Unknown entity: \(item.entity)")
            }
        }

        // grouping: move containers into docker machine, and synthesize a compose hierarchy
        if var dockerMachineItem {
            var flatDockerItems = [ActivityMonitorItem]()
            for (project, items) in newDockerItems {
                if let project {
                    flatDockerItems.append(
                        ActivityMonitorItem.synthetic(
                            entity: .compose(project: project),
                            children: items
                        ))
                } else {
                    flatDockerItems.append(contentsOf: items)
                }
            }

            if let dockerEngineItem {
                flatDockerItems.append(dockerEngineItem)
            }
            if let buildkitItem {
                flatDockerItems.append(buildkitItem)
            }

            // synthesize a k8s hierarchy
            var k8sRootItems = [ActivityMonitorItem]()
            if let k8sServicesItem {
                k8sRootItems.append(k8sServicesItem)
            }
            for (ns, items) in newK8sItems {
                k8sRootItems.append(
                    ActivityMonitorItem.synthetic(entity: .k8sNamespace(ns), children: items))
            }
            if !k8sRootItems.isEmpty {
                flatDockerItems.append(
                    ActivityMonitorItem.synthetic(entity: .k8sGroup, children: k8sRootItems))
            }

            dockerMachineItem.children = flatDockerItems
            newRootItems.append(dockerMachineItem)
        }

        newRootItems.sort(desc: desc)
        items = newRootItems

        lastStats = StatsResult(entries: newEntries, time: now)
    }

    private func entryToItem(entry: StatsEntry, vmModel: VmViewModel, now: ContinuousClock.Instant)
        -> ActivityMonitorItem?
    {
        let lastEntry = lastStats?.entries[entry.id]
        let timeSinceLastRefresh = if let lastStats {
            (now - lastStats.time).seconds
        } else {
            Float?(nil)
        }

        let cpuPercent =
            if let lastEntry,
                let timeSinceLastRefresh,
                entry.cpuUsageUsec >= lastEntry.cpuUsageUsec
            {
                Float(entry.cpuUsageUsec - lastEntry.cpuUsageUsec)
                    / Float(timeSinceLastRefresh * 1_000_000) * 100
            } else {
                Float?(nil)
            }

        let diskRwBytes =
            if let lastEntry,
                entry.diskReadBytes >= lastEntry.diskReadBytes,
                entry.diskWriteBytes >= lastEntry.diskWriteBytes
            {
                // scale by refresh interval to get per-second rate
                UInt64(
                    Double(
                        (entry.diskReadBytes - lastEntry.diskReadBytes)
                            + (entry.diskWriteBytes - lastEntry.diskWriteBytes)) / refreshInterval)
            } else {
                UInt64?(nil)
            }

        let children =
            entry.children?.compactMap { entryToItem(entry: $0, vmModel: vmModel, now: now) }
            ?? []

        var entity: ActivityMonitorEntity?
        switch entry.entity {
        case .machine(let id):
            if let machine = vmModel.containers?.first(where: { $0.record.id == id }) {
                entity = .machine(record: machine.record)
            }
        case .container(let id):
            if let container = vmModel.dockerContainers?.first(where: { $0.id == id }) {
                entity = .container(container: container)
            }
        case .service("dockerd"):
            entity = .dockerEngine
        case .service("buildkit"):
            entity = .buildkit
        case .service("k8s"):
            entity = .k8sServices
        default:
            break
        }
        guard let entity else {
            return nil
        }

        return ActivityMonitorItem(
            entity: entity,
            cpuPercent: cpuPercent,
            memoryBytes: entry.memoryBytes,
            diskRwBytes: diskRwBytes,
            children: children
        )
    }

    func reSort(desc: AKSortDescriptor) {
        items.sort(desc: desc)
        items = items
    }
}

private extension [ActivityMonitorItem] {
    // recursive
    mutating func sort(desc: AKSortDescriptor) {
        // these are structs so we need to mutate in-place
        for index in self.indices {
            self[index].children?.sort(desc: desc)
        }

        self.sort {
            switch desc.columnId {
            case Columns.cpuPercent:
                if let lhs = $0.cpuPercent,
                    let rhs = $1.cpuPercent,
                    lhs != rhs,
                    // for sorting purposes, clamp cpuPercent < 0.05 to minimize instability
                    !(lhs < 0.05 && rhs < 0.05)
                {
                    return desc.compare(lhs, rhs)
                }
            case Columns.memoryBytes:
                let lhs = $0.memoryBytes
                let rhs = $1.memoryBytes
                if lhs != rhs {
                    return desc.compare(lhs, rhs)
                }
            case Columns.diskRwBytes:
                if let lhs = $0.diskRwBytes, let rhs = $1.diskRwBytes, lhs != rhs {
                    return desc.compare(lhs, rhs)
                }
            default:
                break
            }

            return desc.compare($0.id, $1.id)
        }
    }
}

private extension Duration {
    var seconds: Float {
        // attoseconds -> femtoseconds -> picoseconds -> nanoseconds (thanks apple)
        return Float(components.seconds) + Float(components.attoseconds) * 1e-18
    }
}
