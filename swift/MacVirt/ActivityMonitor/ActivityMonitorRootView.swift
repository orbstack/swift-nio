//
//  ActivityMonitorRootView.swift
//  MacVirt
//
//  Created by Danny Lin on 4/7/25.
//

import Defaults
import SwiftUI

private struct ActivityMonitorItem: AKListItem, Equatable, Identifiable {
    let id: ActivityMonitorID
    let entity: ActivityMonitorEntity?

    let cpuPercent: Float?
    let memoryBytes: UInt64
    let diskRwBytes: UInt64?

    let numProcesses: UInt64

    var children: [ActivityMonitorItem]?

    var listChildren: [any AKListItem]? { children }
    var textLabel: String? {
        switch entity {
        case .machine(let record):
            return record.name
        case .container(let container):
            return container.userName
        case .compose(let project):
            return project
        case nil:
            return nil
        }
    }
}

private enum ActivityMonitorID: Equatable, Hashable, Comparable {
    // cgroupPath > pid
    case cgroupPath(String)
    case pid(UInt32)
    case composeProject(String)

    static func < (lhs: ActivityMonitorID, rhs: ActivityMonitorID) -> Bool {
        let lStr: String
        let rStr: String
        switch lhs {
        case .cgroupPath(let path):
            lStr = path
        case .pid(let pid):
            lStr = "\(pid)"
        case .composeProject(let project):
            lStr = project
        }
        switch rhs {
        case .cgroupPath(let path):
            rStr = path
        case .pid(let pid):
            rStr = "\(pid)"
        case .composeProject(let project):
            rStr = project
        }
        return lStr < rStr
    }
}

private enum ActivityMonitorEntity: Equatable {
    case machine(record: ContainerRecord)
    case container(container: DKContainer)
    case compose(project: String)
}

private let initialRefreshInterval = 0.5  // seconds
private let refreshInterval = 1.5  // seconds

private let nsecPerSec = 1e9

private enum Columns {
    static let name = "name"
    static let cpuPercent = "cpuPercent"
    static let memoryBytes = "memoryBytes"
    static let diskRwBytes = "diskRwBytes"
    static let numProcesses = "numProcesses"
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
                flat: false, autosaveName: Defaults.Keys.activityMonitor_autosaveOutline,
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
                                    Text(record.name)
                                }

                            case .container(let container):
                                DockerContainerImage(container: container)
                                    .scaleEffect(0.5)  // 32px -> 16px
                                    .frame(width: 16, height: 16)
                                Text(container.userName)

                            case .compose(let project):
                                DockerComposeGroupImage(project: project)
                                    .scaleEffect(0.5)  // 32px -> 16px
                                    .frame(width: 16, height: 16)
                                Text(project)

                            case nil:
                                EmptyView()
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
                    akColumn(
                        id: Columns.numProcesses, title: "Processes", width: 85, alignment: .right
                    ) { item in
                        Text(item.numProcesses.formatted(.number))
                            .frame(maxWidth: .infinity, alignment: .trailing)
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

@MainActor
private class ActivityMonitorViewModel: ObservableObject {
    private var lastEntries: [StatsID: StatsEntry] = [:]

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

        var newRootItems = [ActivityMonitorItem]()
        var newDockerItems = [String?: [ActivityMonitorItem]]()
        var dockerMachineItem: ActivityMonitorItem?
        for entry in newEntries.values {
            let item = entryToItem(entry: entry, desc: desc, vmModel: vmModel)
            switch item.entity {
            case .machine(let record):
                if record.id == ContainerIds.docker {
                    dockerMachineItem = item
                } else {
                    newRootItems.append(item)
                }
            case .container(let container):
                let projectKey = container.composeProject
                if var items = newDockerItems[projectKey] {
                    items.append(item)
                    newDockerItems[projectKey] = items
                } else {
                    newDockerItems[projectKey] = [item]
                }
            default:
                fatalError("unreachable entity")
            }
        }

        // grouping: move containers into docker machine, and synthesize a compose hierarchy
        if var dockerMachineItem {
            var flatDockerItems = [ActivityMonitorItem]()
            for (project, items) in newDockerItems {
                if let project {
                    // manual reduce loop to deal with nil reduction
                    var cpuPercent: Float? = nil
                    var memoryBytes: UInt64 = 0
                    var diskRwBytes: UInt64? = nil
                    var numProcesses: UInt64 = 0
                    for item in items {
                        if let newCpuPercent = item.cpuPercent {
                            cpuPercent = cpuPercent.map { $0 + newCpuPercent } ?? newCpuPercent
                        }
                        memoryBytes += item.memoryBytes
                        if let newDiskRwBytes = item.diskRwBytes {
                            diskRwBytes = diskRwBytes.map { $0 + newDiskRwBytes } ?? newDiskRwBytes
                        }
                        numProcesses += item.numProcesses
                    }

                    flatDockerItems.append(
                        ActivityMonitorItem(
                            id: .composeProject(project),
                            entity: .compose(project: project),
                            cpuPercent: cpuPercent,
                            memoryBytes: memoryBytes,
                            diskRwBytes: diskRwBytes,
                            numProcesses: numProcesses,
                            children: items
                        ))
                } else {
                    flatDockerItems.append(contentsOf: items)
                }
            }
            dockerMachineItem.children = flatDockerItems
            newRootItems.append(dockerMachineItem)
        }

        newRootItems.sort(desc: desc)
        items = newRootItems

        lastEntries = newEntries
    }

    private func entryToItem(entry: StatsEntry, desc: AKSortDescriptor, vmModel: VmViewModel)
        -> ActivityMonitorItem
    {
        let lastEntry = lastEntries[entry.id]

        let cpuPercent =
            if let lastEntry,
                entry.cpuUsageUsec >= lastEntry.cpuUsageUsec
            {
                Float(entry.cpuUsageUsec - lastEntry.cpuUsageUsec)
                    / Float(refreshInterval * 1_000_000) * 100
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
            entry.children?.map { entryToItem(entry: $0, desc: desc, vmModel: vmModel) } ?? []

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
        }

        let aid: ActivityMonitorID
        switch entry.id {
        case .cgroupPath(let path):
            aid = .cgroupPath(path)
        case .pid(let pid):
            aid = .pid(pid)
        }

        return ActivityMonitorItem(
            id: aid,
            entity: entity,
            cpuPercent: cpuPercent,
            memoryBytes: entry.memoryBytes,
            diskRwBytes: diskRwBytes,
            numProcesses: entry.numProcesses,
            children: children
        )
    }

    func reSort(desc: AKSortDescriptor) {
        items.sort(desc: desc)
        items = items
    }
}

fileprivate extension [ActivityMonitorItem] {
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
