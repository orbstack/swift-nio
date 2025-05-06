//
//  ActivityMonitorRootView.swift
//  MacVirt
//
//  Created by Danny Lin on 4/7/25.
//

import SwiftUI
import Defaults

private struct ActivityMonitorItem: AKListItem, Equatable, Identifiable {
    let id: StatsID
    let cpuPercent: Float?
    let memoryBytes: UInt64
    let diskRwBytes: UInt64?
    
    var listChildren: [any AKListItem]? { nil }
    var textLabel: String? { nil }
}

private let refreshInterval = 1.5

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
    @State private var selection: Set<StatsID> = []
    @State private var sort = AKSortDescriptor(columnId: Columns.cpuPercent, ascending: false)

    var body: some View {
        StateWrapperView {
            AKList(AKSection.single(model.items), selection: $selection, sort: $sort, rowHeight: 24, flat: false, autosaveName: Defaults.Keys.activityMonitor_autosaveOutline, columns: [
                akColumn(id: Columns.name, title: "Name", alignment: .left) { item in
                    if case let .cgroupPath(id) = item.id {
                        Text(id)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                },
                akColumn(id: Columns.cpuPercent, title: "CPU %", alignment: .right) { item in
                    if let cpuPercent = item.cpuPercent {
                        Text(cpuPercent.formatted(.number.precision(.fractionLength(1))))
                            .frame(maxWidth: .infinity, alignment: .trailing)
                    }
                },
                akColumn(id: Columns.memoryBytes, title: "Memory", alignment: .right) { item in
                    Text(ByteCountFormatter.string(fromByteCount: Int64(item.memoryBytes), countStyle: .memory))
                        .frame(maxWidth: .infinity, alignment: .trailing)
                },
                akColumn(id: Columns.diskRwBytes, title: "Disk I/O", alignment: .right) { item in
                    if let diskRwBytes = item.diskRwBytes {
                        Text(ByteCountFormatter.string(fromByteCount: Int64(diskRwBytes), countStyle: .file))
                            .frame(maxWidth: .infinity, alignment: .trailing)
                    }
                },
            ])
            .onAppear {
                Task { @MainActor in
                    await model.refresh(vmModel: vmModel, sort: sort)
                }
            }
            .onChange(of: sort) { newSort in
                model.reSort(sort: newSort)
            }
            .onReceive(timer) { _ in
                Task { @MainActor in
                    await model.refresh(vmModel: vmModel, sort: sort)
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

    func refresh(vmModel: VmViewModel, sort: AKSortDescriptor) async {
        var newStats: StatsResponse!
        do {
            newStats = try await vmModel.tryGetStats(GetStatsRequest(includeProcessCgPaths: []))
        } catch {
            return
        }
        let newEntries = Dictionary(uniqueKeysWithValues: newStats.entries.map { ($0.id, $0) })

        let newItems = newEntries.map {
            let lastEntry = lastEntries[$0.key]
            let cpuPercent = if let lastEntry {
                if $0.value.cpuUsageUsec >= lastEntry.cpuUsageUsec {
                    Float($0.value.cpuUsageUsec - lastEntry.cpuUsageUsec) / Float(refreshInterval * 1_000_000) * 100
                } else {
                    Float?(nil)
                }
            } else {
                Float?(nil)
            }
            let diskRwBytes = if let lastEntry {
                if $0.value.diskReadBytes >= lastEntry.diskReadBytes && $0.value.diskWriteBytes >= lastEntry.diskWriteBytes {
                    ($0.value.diskReadBytes - lastEntry.diskReadBytes) + ($0.value.diskWriteBytes - lastEntry.diskWriteBytes)
                } else {
                    UInt64?(nil)
                }
            } else {
                UInt64?(nil)
            }

            return ActivityMonitorItem(id: $0.key, cpuPercent: cpuPercent, memoryBytes: $0.value.memoryBytes, diskRwBytes: diskRwBytes)
        }
        items = Self.sort(items: newItems, sort: sort)

        lastEntries = newEntries
    }

    func reSort(sort: AKSortDescriptor) {
        items = Self.sort(items: items, sort: sort)
    }

    private static func sort(items: [ActivityMonitorItem], sort: AKSortDescriptor) -> [ActivityMonitorItem] {
        return items.sorted {
            switch sort.columnId {
            case Columns.cpuPercent:
                if let lhs = $0.cpuPercent, let rhs = $1.cpuPercent, lhs != rhs {
                    return sort.compare(lhs, rhs)
                }
            case Columns.memoryBytes:
                let lhs = $0.memoryBytes
                let rhs = $1.memoryBytes
                if lhs != rhs {
                    return sort.compare(lhs, rhs)
                }
            case Columns.diskRwBytes:
                if let lhs = $0.diskRwBytes, let rhs = $1.diskRwBytes, lhs != rhs {
                    return sort.compare(lhs, rhs)
                }
            default:
                break
            }

            return sort.compare($0.id, $1.id)
        }
    }
}
