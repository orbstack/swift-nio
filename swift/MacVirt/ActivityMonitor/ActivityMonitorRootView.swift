//
//  ActivityMonitorRootView.swift
//  MacVirt
//
//  Created by Danny Lin on 4/7/25.
//

import Charts
import Defaults
import SwiftUI

private let historyGraphSize = 50

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
    case dockerGroup
    case dockerEngine
    case buildkit
    case machinesGroup
}

private enum ActivityMonitorEntity: Identifiable, Comparable, CustomStringConvertible {
    case machine(record: ContainerRecord)
    case container(container: DKContainer)
    case compose(project: String)

    // synthetics
    case k8sGroup
    case k8sNamespace(String)
    case k8sServices
    case dockerGroup
    case dockerEngine
    case buildkit
    case machinesGroup

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
        case .dockerGroup:
            return .dockerGroup
        case .dockerEngine:
            return .dockerEngine
        case .buildkit:
            return .buildkit
        case .machinesGroup:
            return .machinesGroup
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
            return "System Services"
        case .dockerGroup:
            return "Containers"
        case .dockerEngine:
            return "Engine"
        case .buildkit:
            return "Builds"
        case .machinesGroup:
            return "Machines"
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

private struct CpuHistoryGraphItem: Hashable {
    let index: Int
    let cpuPercent: Float?
}

private struct MemoryHistoryGraphItem: Hashable {
    let index: Int
    let memoryBytes: UInt64?
}

struct ActivityMonitorRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var actionTracker: ActionTracker

    private let timer = Timer.publish(every: refreshInterval, on: .main, in: .common).autoconnect()

    @StateObject private var model = ActivityMonitorViewModel()
    @State private var selection: Set<ActivityMonitorID> = []
    @State private var sort = AKSortDescriptor(columnId: Columns.cpuPercent, ascending: false)

    var body: some View {
        StateWrapperView {
            AKList(
                AKSection.single(model.items), selection: $selection, sort: $sort,
                rowHeight: 24,
                flat: false, expandByDefault: true,
                autosaveName: Defaults.Keys.activityMonitor_autosaveOutline,
                columns: [
                    akColumn(id: Columns.name, title: "Name", width: 200, alignment: .left) {
                        item in
                        HStack(spacing: 6) {
                            switch item.entity {
                            case .machine(let record):
                                Image("distro_\(record.image.distro)")
                                    .resizable()
                                    .aspectRatio(contentMode: .fit)
                                    .frame(width: 16, height: 16)

                            case .container(let container):
                                DockerContainerImage(container: container)
                                    .scaleEffect(0.5)  // 32px -> 16px
                                    .frame(width: 16, height: 16)

                            case .compose(let project):
                                DockerComposeGroupImage(project: project)
                                    .scaleEffect(0.5)  // 32px -> 16px
                                    .frame(width: 16, height: 16)

                            case .k8sGroup:
                                K8sIcon()
                                    .scaleEffect(0.5)  // 32px -> 16px
                                    .frame(width: 16, height: 16)

                            case .dockerGroup:
                                Image(systemName: "shippingbox.fill")
                                    .resizable()
                                    .aspectRatio(contentMode: .fit)
                                    .frame(width: 16, height: 16)

                            case .machinesGroup:
                                Image(systemName: "desktopcomputer")
                                    .resizable()
                                    .aspectRatio(contentMode: .fit)
                                    .frame(width: 16, height: 16)

                            case .k8sNamespace:
                                Group {}

                            case .k8sServices:
                                Group {}

                            case .dockerEngine:
                                Group {}

                            default:
                                Spacer()
                                    .frame(width: 16, height: 16)
                            }

                            Text(item.entity.description)
                        }
                        .padding(.leading, 2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .lineLimit(1)
                        .akListContextMenu {
                            Button("Stop") {
                                if selection.contains(item.id) {
                                    stopOne(id: item.id)
                                } else {
                                    stopAllSelected(stopAction: stopOne)
                                }
                            }

                            Button("Kill") {
                                if selection.contains(item.id) {
                                    killOne(id: item.id)
                                } else {
                                    stopAllSelected(stopAction: killOne)
                                }
                            }
                        }
                    },
                    akColumn(
                        id: Columns.cpuPercent, title: "CPU %", width: 75, alignment: .right
                    ) {
                        item in
                        if let cpuPercent = item.cpuPercent {
                            Text(cpuPercent.formatted(.number.precision(.fractionLength(1))))
                                .frame(maxWidth: .infinity, alignment: .trailing)
                        }
                    },
                    akColumn(
                        id: Columns.memoryBytes, title: "Memory", width: 75, alignment: .right
                    ) { item in
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
            .inspectorView {
                ZStack(alignment: .topLeading) {
                    VStack(alignment: .leading) {
                        Text("CPU")
                            .font(.headline)
                        Chart(model.cpuHistoryGraph, id: \.self) { item in
                            if let cpuPercent = item.cpuPercent {
                                LineMark(
                                    x: .value("Time", item.index), y: .value("CPU %", cpuPercent)
                                )
                                .foregroundStyle(.green)
                                .lineStyle(StrokeStyle(lineWidth: 1.5))

                                AreaMark(
                                    x: .value("Time", item.index), y: .value("CPU %", cpuPercent)
                                )
                                .foregroundStyle(
                                    LinearGradient(
                                        gradient: Gradient(colors: [
                                            Color.green.opacity(0.8),
                                            Color.green.opacity(0.1),
                                        ]),
                                        startPoint: .top,
                                        endPoint: .bottom
                                    )
                                )
                            }
                        }
                        .chartXScale(domain: [0, historyGraphSize - 1])
                        .chartYScale(domain: [
                            0,
                            max(
                                200,
                                model.cpuHistoryGraph.max(by: {
                                    $0.cpuPercent ?? 0 < $1.cpuPercent ?? 0
                                })?.cpuPercent ?? 0),
                        ])
                        .chartXAxis {
                        }
                        .chartYAxis {
                        }
                        .frame(height: 150)
                        .border(.gray.opacity(0.5))

                        Text("Memory")
                            .font(.headline)
                            .padding(.top, 20)
                        let memoryLimit = (vmModel.config?.memoryMib ?? 0) * 1_048_576
                        Chart(model.memoryHistoryGraph, id: \.self) { item in
                            if let memoryBytes = item.memoryBytes {
                                LineMark(
                                    x: .value("Time", item.index), y: .value("Memory", memoryBytes)
                                )
                                .foregroundStyle(.blue)
                                .lineStyle(StrokeStyle(lineWidth: 1.5))

                                AreaMark(
                                    x: .value("Time", item.index), y: .value("Memory", memoryBytes)
                                )
                                .foregroundStyle(
                                    LinearGradient(
                                        gradient: Gradient(colors: [
                                            Color.blue.opacity(0.8),
                                            Color.blue.opacity(0.1),
                                        ]),
                                        startPoint: .top,
                                        endPoint: .bottom
                                    )
                                )
                            }
                        }
                        .chartXScale(domain: [0, historyGraphSize - 1])
                        .chartYScale(domain: [
                            0,
                            max(
                                memoryLimit,
                                model.memoryHistoryGraph.max(by: {
                                    $0.memoryBytes ?? 0 < $1.memoryBytes ?? 0
                                })?.memoryBytes ?? 0),
                        ])
                        .chartXAxis {
                        }
                        .chartYAxis {
                        }
                        .frame(height: 150)
                        .border(.gray.opacity(0.5))
                    }
                    .padding(20)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            }
        }
        .navigationTitle("Activity Monitor")
        .onReceive(vmModel.toolbarActionRouter) { action in
            switch action {
            case .activityMonitorStop:
                stopAllSelected(stopAction: stopOne)
            }
        }
        .onAppear {
            vmModel.activityMonitorStopEnabled = !selection.isEmpty
        }
        .onChange(of: selection) { newSelection in
            vmModel.activityMonitorStopEnabled = !newSelection.isEmpty
        }
    }

    private func stopOne(id: ActivityMonitorID) {
        Task {
            switch id {
            case .machine(let id):
                if let machine = vmModel.containers?.first(where: { $0.record.id == id }) {
                    await vmModel.tryStopContainer(machine.record)
                }
            case .container(let id):
                await vmModel.tryDockerContainerStop(id)
            case .compose(let project):
                await vmModel.tryDockerComposeStop(.compose(project: project))

            case .k8sGroup, .k8sServices:
                await actionTracker.with(cid: .k8sGroup, action: .stop) {
                    await vmModel.tryStartStopK8s(enable: false)
                }

            case .dockerGroup, .dockerEngine:
                if let dockerMachine = vmModel.containers?.first(where: {
                    $0.id == ContainerIds.docker
                }) {
                    await vmModel.tryStopContainer(dockerMachine.record)
                }

            case .k8sNamespace:
                // TODO
                break

            case .machinesGroup:
                for machine in vmModel.containers ?? [] {
                    if machine.record.running && !machine.record.builtin {
                        Task {
                            await vmModel.tryStopContainer(machine.record)
                        }
                    }
                }

            default:
                break
            }
        }
    }

    private func killOne(id: ActivityMonitorID) {
        Task {
            switch id {
            case .container(let id):
                await vmModel.tryDockerContainerKill(id)
            case .compose(let project):
                await vmModel.tryDockerComposeKill(.compose(project: project))

            default:
                return stopOne(id: id)
            }
        }
    }

    private func stopAllSelected(stopAction: (ActivityMonitorID) -> Void) {
        // some special cases:
        // - if stopping .dockerGroup or .dockerEngine: skip .container .compose .k8sGroup .k8sServices .k8sNamespace
        // - if stopping .k8sGroup or .k8sServices: skip .k8sNamespace
        // - if stopping .machinesGroup: skip .machine

        // that makes it easier to do this in a few passes.

        // 1. docker
        if selection.contains(.dockerGroup) || selection.contains(.dockerEngine) {
            stopAction(.dockerGroup)
        } else {
            // 2. containers
            // must do this before k8s because that triggers a restart
            for id in selection {
                if case .container = id {
                    stopAction(id)
                }
            }

            // 3. compose
            // must do this after containers to avoid errors in case some stopped containers are part of a compose group
            for id in selection {
                if case .compose = id {
                    stopAction(id)
                }
            }

            // 4. k8s
            if selection.contains(.k8sGroup) || selection.contains(.k8sServices) {
                stopAction(.k8sGroup)
            } else {
                // 3. k8s namespaces
                for id in selection {
                    if case .k8sNamespace = id {
                        stopAction(id)
                    }
                }
            }
        }

        // 2. machines
        if selection.contains(.machinesGroup) {
            // stop all machines
            stopAction(.machinesGroup)
        } else {
            // 3. machines
            for id in selection {
                if case .machine = id {
                    stopAction(id)
                }
            }
        }
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

    private var cpuHistory: [Float?] = Array(repeating: nil, count: historyGraphSize)
    @Published var cpuHistoryGraph: [CpuHistoryGraphItem] = []

    private var memoryHistory: [UInt64?] = Array(repeating: nil, count: historyGraphSize)
    @Published var memoryHistoryGraph: [MemoryHistoryGraphItem] = []

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
        var newMachineItems = [ActivityMonitorItem]()
        var dockerMachineItem: ActivityMonitorItem?
        var dockerEngineItem: ActivityMonitorItem?
        var buildkitItem: ActivityMonitorItem?
        var k8sServicesItem: ActivityMonitorItem?
        for entry in newEntries.values {
            let item = entryToItem(entry: entry, vmModel: vmModel, now: now)
            guard let item else { continue }

            switch item.entity {
            case .machine:
                newMachineItems.append(item)
            case .container(let container):
                if let k8sNs = container.k8sNamespace {
                    newK8sItems[k8sNs, default: []].append(item)
                } else {
                    newDockerItems[container.composeProject, default: []].append(item)
                }
            case .dockerGroup:
                dockerMachineItem = item
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
                // subtract from containers group
                let k8sItem = ActivityMonitorItem.synthetic(
                    entity: .k8sGroup, children: k8sRootItems)
                // sample times are not exactly aligned so this may overflow
                if let dockerValue = dockerMachineItem.cpuPercent,
                    let k8sValue = k8sItem.cpuPercent, dockerValue >= k8sValue
                {
                    dockerMachineItem.cpuPercent! -= k8sValue
                }
                if dockerMachineItem.memoryBytes >= k8sItem.memoryBytes {
                    dockerMachineItem.memoryBytes -= k8sItem.memoryBytes
                }
                if let dockerValue = dockerMachineItem.diskRwBytes,
                    let k8sValue = k8sItem.diskRwBytes, dockerValue >= k8sValue
                {
                    dockerMachineItem.diskRwBytes! -= k8sValue
                }
                newRootItems.append(k8sItem)
            }

            dockerMachineItem.children = flatDockerItems
            newRootItems.append(dockerMachineItem)
        }

        // grouping: make synthetic machines group item
        if !newMachineItems.isEmpty {
            newRootItems.append(
                ActivityMonitorItem.synthetic(entity: .machinesGroup, children: newMachineItems))
        }

        newRootItems.sort(desc: desc)
        items = newRootItems

        lastStats = StatsResult(entries: newEntries, time: now)

        let totalCpuPercent = items.reduce(0) { $0 + ($1.cpuPercent ?? 0) }
        updateCpuHistoryGraph(newSample: totalCpuPercent)

        let totalMemoryBytes = items.reduce(0) { $0 + $1.memoryBytes }
        updateMemoryHistoryGraph(newSample: totalMemoryBytes)
    }

    private func updateCpuHistoryGraph(newSample: Float) {
        if cpuHistory.count >= historyGraphSize {
            cpuHistory.removeFirst()
        }
        cpuHistory.append(newSample)
        cpuHistoryGraph = cpuHistory.enumerated().map {
            CpuHistoryGraphItem(index: $0, cpuPercent: $1)
        }
    }

    private func updateMemoryHistoryGraph(newSample: UInt64) {
        if memoryHistory.count >= historyGraphSize {
            memoryHistory.removeFirst()
        }
        memoryHistory.append(newSample)
        memoryHistoryGraph = memoryHistory.enumerated().map {
            MemoryHistoryGraphItem(index: $0, memoryBytes: $1)
        }
    }

    private func entryToItem(entry: StatsEntry, vmModel: VmViewModel, now: ContinuousClock.Instant)
        -> ActivityMonitorItem?
    {
        let lastEntry = lastStats?.entries[entry.id]
        let timeSinceLastRefresh =
            if let lastStats {
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
        case .machine(ContainerIds.docker):
            entity = .dockerGroup
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

extension [ActivityMonitorItem] {
    // recursive
    fileprivate mutating func sort(desc: AKSortDescriptor) {
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

            return desc.compare($0.textLabel!, $1.textLabel!)
        }
    }
}

extension Duration {
    fileprivate var seconds: Float {
        // attoseconds -> femtoseconds -> picoseconds -> nanoseconds (thanks apple)
        return Float(components.seconds) + Float(components.attoseconds) * 1e-18
    }
}
