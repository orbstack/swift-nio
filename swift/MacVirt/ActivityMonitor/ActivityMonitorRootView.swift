//
//  ActivityMonitorRootView.swift
//  MacVirt
//
//  Created by Danny Lin on 4/7/25.
//

import Charts
import Defaults
import SwiftUI

private let historySize = 50

private struct ActivityMonitorItem: AKListItem, Equatable, Identifiable {
    let entity: ActivityMonitorEntity
    var id: ActivityMonitorID { entity.id }

    var cpuPercent: Float?
    var memoryBytes: UInt64
    var netRxTxBytes: UInt64?
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
        var netRxTxBytes: UInt64? = nil
        var diskRwBytes: UInt64? = nil
        for item in children {
            if let newCpuPercent = item.cpuPercent {
                cpuPercent = (cpuPercent ?? 0) + newCpuPercent
            }
            memoryBytes += item.memoryBytes
            if let newNetRxTxBytes = item.netRxTxBytes {
                netRxTxBytes = (netRxTxBytes ?? 0) + newNetRxTxBytes
            }
            if let newDiskRwBytes = item.diskRwBytes {
                diskRwBytes = (diskRwBytes ?? 0) + newDiskRwBytes
            }
        }

        return ActivityMonitorItem(
            entity: entity, cpuPercent: cpuPercent, memoryBytes: memoryBytes,
            netRxTxBytes: netRxTxBytes,
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
    static let netRxTxBytes = "netRxTxBytes"
    static let diskRwBytes = "diskRwBytes"
}

private struct HistoryGraphItem: Hashable {
    let index: Int
    var value: Float?

    static func += (lhs: inout HistoryGraphItem, rhs: Float?) {
        if let rhs {
            lhs.value = (lhs.value ?? 0) + rhs
        }
    }

    static func -= (lhs: inout HistoryGraphItem, rhs: Float?) {
        if let rhs {
            lhs.value = max(0, (lhs.value ?? 0) - rhs)
        }
    }
}

private struct HistoryGraph: View {
    let trackedEntries: [StatsID: TrackedStatsEntry]
    let modelItems: [ActivityMonitorItem]
    let selection: Set<ActivityMonitorID>

    let key: KeyPath<TrackedStatsEntry, [Float?]>
    let name: String
    let color: Color
    let maxValue: Float
    let alignTo: Float
    let formatter: (Float) -> String

    var body: some View {
        let (graphItems, isTotal) = calculateItems()
        let graphMaxValue = max(
            maxValue,
            graphItems.max(by: {
                $0.value ?? 0 < $1.value ?? 0
            })?.value ?? 0
        ).alignUp(to: alignTo)

        VStack(alignment: .leading) {
            let title = isTotal ? "\(name):" : "\(name) (selected):"
            HStack(alignment: .center) {
                Text(title)
                    .font(.headline)
                Spacer()
                if let lastItem = graphItems.last,
                    let lastValue = lastItem.value
                {
                    Text(formatter(lastValue))
                }
            }

            Chart(graphItems, id: \.self) { item in
                if let value = item.value {
                    LineMark(
                        x: .value("Time", item.index), y: .value(name, value)
                    )
                    .foregroundStyle(color)
                    .lineStyle(StrokeStyle(lineWidth: 1.5))

                    AreaMark(
                        x: .value("Time", item.index), y: .value(name, value)
                    )
                    .foregroundStyle(
                        LinearGradient(
                            gradient: Gradient(colors: [
                                color.opacity(0.8),
                                color.opacity(0.1),
                            ]),
                            startPoint: .top,
                            endPoint: .bottom
                        )
                    )
                }
            }
            .chartXScale(domain: [0, historySize - 1])
            .chartYScale(domain: [
                0,
                graphMaxValue,
            ])
            .chartXAxis {
            }
            .chartYAxis {
            }
            .frame(height: 100)
            .padding(.bottom, 1)  // fix zero line getting clipped
            .clipShape(RoundedRectangle(cornerRadius: 4))  // if values > max, Swift Charts draws out of bounds
            .overlay(RoundedRectangle(cornerRadius: 4).stroke(.gray.opacity(0.5), lineWidth: 1))
        }
    }

    private func findK8sNamespace(containerId: String) -> String? {
        if let k8sGroupItem = modelItems.first(where: { $0.entity.id == .k8sGroup }) {
            return k8sGroupItem.children?.firstNonNil({ item in
                if case .k8sNamespace(let ns) = item.entity,
                    item.children?.contains(where: { $0.entity.id == .container(id: containerId) })
                        ?? false
                {
                    return ns
                }
                return nil
            })
        } else {
            return nil
        }
    }

    private func calculateItems() -> ([HistoryGraphItem], Bool) {
        var items = (0..<historySize).map { HistoryGraphItem(index: $0, value: nil) }
        var numItems = 0
        func addEntry(_ entry: TrackedStatsEntry) {
            let history = entry[keyPath: key]
            for (index, value) in history.enumerated() {
                items[index] += value
            }
            numItems += 1
        }
        func subtractEntry(_ entry: TrackedStatsEntry) {
            let history = entry[keyPath: key]
            for (index, value) in history.enumerated() {
                items[index] -= value
            }
        }

        // add selections
        for tracked in trackedEntries.values {
            switch tracked.entry.entity {
            // docker machine is special once again
            case .machine(ContainerIds.docker):
                if selection.contains(.dockerGroup) {
                    addEntry(tracked)

                    // subtract k8s
                    for tracked in trackedEntries.values {
                        switch tracked.entry.entity {
                        case .service("k8s"):
                            subtractEntry(tracked)
                        case .container(let id):
                            if findK8sNamespace(containerId: id) != nil {
                                subtractEntry(tracked)
                            }
                        default:
                            break
                        }
                    }
                }
            // other machines
            case .machine(let id):
                if selection.contains(.machine(id: id)) || selection.contains(.machinesGroup) {
                    addEntry(tracked)
                }

            // included in docker machine, unless it's k8s in which case it was subtracted above
            case .container(let id):
                let k8sNamespace = findK8sNamespace(containerId: id)
                if k8sNamespace != nil || !selection.contains(.dockerGroup) {
                    if selection.contains(.container(id: id)) {
                        addEntry(tracked)
                        // complicated check for compose children
                    } else if let dockerGroupItem = modelItems.first(where: {
                        $0.entity.id == .dockerGroup
                    }),
                        let composeItem = dockerGroupItem.children?.first(where: {
                            if case .compose = $0.entity {
                                return $0.children?.contains(where: {
                                    return $0.entity.id == .container(id: id)
                                }) ?? false
                            }
                            return false
                        }) ?? nil, selection.contains(composeItem.id)
                    {
                        addEntry(tracked)
                        // complicated check for k8s children
                    } else if let k8sNamespace {
                        if selection.contains(.k8sGroup)
                            || selection.contains(.k8sNamespace(k8sNamespace))
                        {
                            addEntry(tracked)
                        }
                    }
                }
            // included in docker machine
            case .service("dockerd"):
                if selection.contains(.dockerEngine) && !selection.contains(.dockerGroup) {
                    addEntry(tracked)
                }
            // included in docker machine
            case .service("buildkit"):
                if selection.contains(.buildkit) && !selection.contains(.dockerGroup) {
                    addEntry(tracked)
                }
            // included in docker machine
            case .service("k8s"):
                if selection.contains(.k8sGroup) || selection.contains(.k8sServices) {
                    addEntry(tracked)
                }

            default:
                break
            }
        }

        // empty selection = all
        // we don't check because there can be IDs in selection that no longer exist
        let isTotal = numItems == 0
        if isTotal {
            for tracked in trackedEntries.values {
                // we ONLY need to sum machines to get the total, because all .service and .container are under docker machine
                if case .machine = tracked.entry.entity {
                    addEntry(tracked)
                }
            }
        }

        return (items, isTotal)
    }
}

private let memoryByteCountFormatter = {
    let formatter = ByteCountFormatter()
    formatter.countStyle = .memory
    formatter.allowsNonnumericFormatting = false  // "Zero KB" -> "0 KB"
    // no "bytes" because it's too long
    formatter.allowedUnits = [
        .useKB, .useMB, .useGB, .useTB, .usePB, .useEB, .useZB, .useYBOrHigher,
    ]
    formatter.countStyle = .decimal  // so we never get 4-digit numbers between 1000-1023 (shorter for UI)
    return formatter
}()

private func formatCpuPercent(_ value: Float) -> String {
    return value.formatted(.number.precision(.fractionLength(1)))
}

private func formatMemoryBytes(_ value: Int64) -> String {
    return memoryByteCountFormatter.string(fromByteCount: value)
}

private func formatNetRxTxBytes(_ value: Int64) -> String {
    return "\(memoryByteCountFormatter.string(fromByteCount: value))/s"
}

private func formatDiskRwBytes(_ value: Int64) -> String {
    return "\(memoryByteCountFormatter.string(fromByteCount: value))/s"
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
                    // takes all remaining space
                    akColumn(id: Columns.name, title: "Name", width: 150, alignment: .left) {
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
                            Button {
                                if selection.contains(item.id) {
                                    stopOne(id: item.id)
                                } else {
                                    stopAllSelected(stopAction: stopOne)
                                }
                            } label: {
                                Label("Stop", systemImage: "stop")
                            }

                            Button {
                                if selection.contains(item.id) {
                                    killOne(id: item.id)
                                } else {
                                    stopAllSelected(stopAction: killOne)
                                }
                            } label: {
                                Label("Kill", systemImage: "xmark.octagon")
                            }
                        }
                    },
                    akColumn(
                        id: Columns.cpuPercent, title: "CPU %", width: 56, alignment: .right
                    ) {
                        item in
                        if let cpuPercent = item.cpuPercent {
                            Text(formatCpuPercent(cpuPercent))
                                .frame(maxWidth: .infinity, alignment: .trailing)
                        }
                    },
                    akColumn(
                        id: Columns.memoryBytes, title: "Memory", width: 72, alignment: .right
                    ) { item in
                        Text(formatMemoryBytes(Int64(item.memoryBytes)))
                            .frame(maxWidth: .infinity, alignment: .trailing)
                    },
                    akColumn(
                        id: Columns.netRxTxBytes, title: "Network", width: 72, alignment: .right
                    ) { item in
                        if let netRxTxBytes = item.netRxTxBytes {
                            Text(formatNetRxTxBytes(Int64(netRxTxBytes)))
                                .frame(maxWidth: .infinity, alignment: .trailing)
                        }
                    },
                    akColumn(
                        id: Columns.diskRwBytes, title: "Disk", width: 72, alignment: .right
                    ) {
                        item in
                        if let diskRwBytes = item.diskRwBytes {
                            Text(formatDiskRwBytes(Int64(diskRwBytes)))
                                .frame(maxWidth: .infinity, alignment: .trailing)
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
            .onChange(of: sort) { _, newSort in
                model.reSort(desc: newSort)
            }
            .onReceive(timer) { _ in
                Task { @MainActor in
                    await model.refresh(vmModel: vmModel, desc: sort)
                }
            }
            .inspectorView {
                ScrollView {
                    ZStack(alignment: .topLeading) {
                        VStack(alignment: .leading, spacing: 20) {
                            // AttributeGraph doesn't work across NSHostingView boundaries, so we have to pass these as args
                            HistoryGraph(
                                trackedEntries: model.lastStats?.trackedEntries ?? [:],
                                modelItems: model.items,

                                selection: selection,
                                key: \.cpuHistory,
                                name: "CPU",
                                color: .red,
                                maxValue: 100,
                                alignTo: 100,
                                formatter: { "\(formatCpuPercent($0))%" }
                            )

                            let memoryLimit = (vmModel.config?.memoryMib ?? 0) * 1_048_576
                            HistoryGraph(
                                trackedEntries: model.lastStats?.trackedEntries ?? [:],
                                modelItems: model.items,

                                selection: selection,
                                key: \.memoryHistory,
                                name: "Memory",
                                color: .blue,
                                maxValue: Float(memoryLimit),
                                alignTo: 512 * 1_048_576,  // 512 MiB
                                formatter: { formatMemoryBytes(Int64($0)) }
                            )

                            HistoryGraph(
                                trackedEntries: model.lastStats?.trackedEntries ?? [:],
                                modelItems: model.items,

                                selection: selection,
                                key: \.netRxTxBytesHistory,
                                name: "Network",
                                color: .green,
                                maxValue: 32 * 1_048_576,  // 32 MiB
                                alignTo: 32 * 1_048_576,  // 32 MiB
                                formatter: { formatNetRxTxBytes(Int64($0)) }
                            )

                            HistoryGraph(
                                trackedEntries: model.lastStats?.trackedEntries ?? [:],
                                modelItems: model.items,

                                selection: selection,
                                key: \.diskRwBytesHistory,
                                name: "Disk",
                                color: .purple,
                                maxValue: 32 * 1_048_576,  // 32 MiB
                                alignTo: 32 * 1_048_576,  // 32 MiB
                                formatter: { formatDiskRwBytes(Int64($0)) }
                            )
                        }
                        .padding(16)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            }
        }
        .navigationTitle("Activity Monitor")
        .onReceive(vmModel.toolbarActionRouter) { action in
            switch action {
            case .activityMonitorStop:
                stopAllSelected(stopAction: stopOne)
            default:
                break
            }
        }
        .onAppear {
            vmModel.activityMonitorStopEnabled = !selection.isEmpty
        }
        .onChange(of: selection) { _, newSelection in
            vmModel.activityMonitorStopEnabled = !newSelection.isEmpty
        }
    }

    private func stopOne(id: ActivityMonitorID) {
        Task {
            switch id {
            case .machine(let id):
                if let machine = vmModel.machines?[id] {
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
                if let dockerMachine = vmModel.dockerMachine {
                    await vmModel.tryStopContainer(dockerMachine.record)
                }

            case .k8sNamespace:
                // TODO
                break

            case .machinesGroup:
                vmModel.machines?.values.forEach { machine in
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

private struct TrackedStatsEntry {
    let entry: StatsEntry
    var cpuHistory: [Float?]
    var memoryHistory: [Float?]
    var netRxTxBytesHistory: [Float?]
    var diskRwBytesHistory: [Float?]
}

private struct StatsResult {
    let trackedEntries: [StatsID: TrackedStatsEntry]
    let time: SuspendingClock.Instant
}

@MainActor
private class ActivityMonitorViewModel: ObservableObject {
    @Published var lastStats: StatsResult? = nil
    @Published var items: [ActivityMonitorItem] = []

    func refresh(vmModel: VmViewModel, desc: AKSortDescriptor) async {
        var newStats: StatsResponse!
        do {
            newStats = try await vmModel.tryGetStats(GetStatsRequest(includeProcessCgPaths: []))
        } catch {
            return
        }
        var newTrackedEntries = [StatsID: TrackedStatsEntry]()
        newTrackedEntries.reserveCapacity(newStats.entries.count)

        // CLOCK_MONOTONIC is a better fit than CLOCK_BOOTTIME because the system sleeping in the middle of a refresh won't skew the results
        let now = SuspendingClock.now

        var newRootItems = [ActivityMonitorItem]()
        var newDockerItems = [String?: [ActivityMonitorItem]]()
        var newK8sItems = [String: [ActivityMonitorItem]]()
        var newMachineItems = [ActivityMonitorItem]()
        var dockerMachineItem: ActivityMonitorItem?
        var dockerEngineItem: ActivityMonitorItem?
        var buildkitItem: ActivityMonitorItem?
        var k8sServicesItem: ActivityMonitorItem?
        for entry in newStats.entries {
            guard
                let item = entryToItem(
                    entry: entry, now: now, vmModel: vmModel, newTrackedEntries: &newTrackedEntries)
            else { continue }

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

        lastStats = StatsResult(trackedEntries: newTrackedEntries, time: now)
    }

    private func entryToItem(
        entry: StatsEntry, now: SuspendingClock.Instant, vmModel: VmViewModel,
        newTrackedEntries: inout [StatsID: TrackedStatsEntry]
    )
        -> ActivityMonitorItem?
    {
        let tracked = lastStats?.trackedEntries[entry.id]
        let timeSinceLastRefresh =
            if let lastStats {
                (now - lastStats.time).seconds
            } else {
                Float?(nil)
            }

        let cpuPercent =
            if let tracked,
                let timeSinceLastRefresh,
                entry.cpuUsageUsec >= tracked.entry.cpuUsageUsec
            {
                Float(entry.cpuUsageUsec - tracked.entry.cpuUsageUsec)
                    / Float(timeSinceLastRefresh * 1_000_000) * 100
            } else {
                Float?(nil)
            }

        let netRxTxBytes =
            if let tracked,
                let newRx = entry.netRxBytes,
                let newTx = entry.netTxBytes,
                let oldRx = tracked.entry.netRxBytes,
                let oldTx = tracked.entry.netTxBytes,
                newRx >= oldRx,
                newTx >= oldTx
            {
                // scale by refresh interval to get per-second rate
                UInt64(
                    Double(
                        (newRx - oldRx) + (newTx - oldTx))
                        / refreshInterval)
            } else {
                UInt64?(nil)
            }

        let diskRwBytes =
            if let tracked,
                entry.diskReadBytes >= tracked.entry.diskReadBytes,
                entry.diskWriteBytes >= tracked.entry.diskWriteBytes
            {
                // scale by refresh interval to get per-second rate
                UInt64(
                    Double(
                        (entry.diskReadBytes - tracked.entry.diskReadBytes)
                            + (entry.diskWriteBytes - tracked.entry.diskWriteBytes))
                        / refreshInterval)
            } else {
                UInt64?(nil)
            }

        let children =
            entry.children?.compactMap {
                entryToItem(
                    entry: $0, now: now, vmModel: vmModel, newTrackedEntries: &newTrackedEntries)
            }

        var entity: ActivityMonitorEntity?
        switch entry.entity {
        case .machine(ContainerIds.docker):
            entity = .dockerGroup
        case .machine(let id):
            if let machine = vmModel.machines?[id] {
                entity = .machine(record: machine.record)
            }
        case .container(let id):
            if let container = vmModel.dockerContainers?.byId[id] {
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

        // update history
        var historicalEntry = TrackedStatsEntry(
            entry: entry,
            cpuHistory: tracked?.cpuHistory ?? [Float?](repeating: nil, count: historySize),
            memoryHistory: tracked?.memoryHistory ?? [Float?](repeating: nil, count: historySize),
            netRxTxBytesHistory: tracked?.netRxTxBytesHistory
                ?? [Float?](repeating: nil, count: historySize),
            diskRwBytesHistory: tracked?.diskRwBytesHistory
                ?? [Float?](repeating: nil, count: historySize)
        )
        historicalEntry.cpuHistory.removeFirst()
        historicalEntry.cpuHistory.append(cpuPercent)
        // memory history is present starting from the first sample, while cpu history is delayed by 1 sample because it needs a delta calculation
        // to align the graphs, skip adding memory sample if cpu is not present
        if cpuPercent != nil {
            historicalEntry.memoryHistory.removeFirst()
            historicalEntry.memoryHistory.append(Float(entry.memoryBytes))
        }
        if let netRxTxBytes {
            historicalEntry.netRxTxBytesHistory.removeFirst()
            historicalEntry.netRxTxBytesHistory.append(Float(netRxTxBytes))
        }
        if let diskRwBytes {
            historicalEntry.diskRwBytesHistory.removeFirst()
            historicalEntry.diskRwBytesHistory.append(Float(diskRwBytes))
        }
        newTrackedEntries[entry.id] = historicalEntry

        return ActivityMonitorItem(
            entity: entity,
            cpuPercent: cpuPercent,
            memoryBytes: entry.memoryBytes,
            netRxTxBytes: netRxTxBytes,
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

        // switch outside comparator for perf
        switch desc.columnId {
        case Columns.cpuPercent:
            self.sort {
                if let lhs = $0.cpuPercent,
                    let rhs = $1.cpuPercent,
                    lhs != rhs,
                    // for sorting purposes, clamp cpuPercent < 0.05 to minimize instability
                    !(lhs < 0.05 && rhs < 0.05)
                {
                    return desc.compare(lhs, rhs)
                }
                return desc.compare($0.textLabel!, $1.textLabel!)
            }
        case Columns.memoryBytes:
            self.sort {
                let lhs = $0.memoryBytes
                let rhs = $1.memoryBytes
                if lhs != rhs {
                    return desc.compare(lhs, rhs)
                }
                return desc.compare($0.textLabel!, $1.textLabel!)
            }
        case Columns.netRxTxBytes:
            self.sort {
                if let lhs = $0.netRxTxBytes, let rhs = $1.netRxTxBytes, lhs != rhs {
                    return desc.compare(lhs, rhs)
                }
                return desc.compare($0.textLabel!, $1.textLabel!)
            }
        case Columns.diskRwBytes:
            self.sort {
                if let lhs = $0.diskRwBytes, let rhs = $1.diskRwBytes, lhs != rhs {
                    return desc.compare(lhs, rhs)
                }
                return desc.compare($0.textLabel!, $1.textLabel!)
            }
        default:
            self.sort {
                return desc.compare($0.textLabel!, $1.textLabel!)
            }
        }
    }

    fileprivate func findRecursive(id: ActivityMonitorID) -> ActivityMonitorItem? {
        for item in self {
            if item.entity.id == id {
                return item
            }
            if let child = item.children?.findRecursive(id: id) {
                return child
            }
        }
        return nil
    }
}

extension Duration {
    fileprivate var seconds: Float {
        // attoseconds -> femtoseconds -> picoseconds -> nanoseconds (thanks apple)
        return Float(components.seconds) + Float(components.attoseconds) * 1e-18
    }
}

extension Float {
    fileprivate func alignUp(to alignBy: Float) -> Float {
        return (self / alignBy).rounded(.up) * alignBy
    }
}
