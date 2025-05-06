//
//  ActivityMonitorRootView.swift
//  MacVirt
//
//  Created by Danny Lin on 4/7/25.
//

import SwiftUI

private struct ActivityMonitorItem: AKListItem, Equatable, Identifiable {
    let id: StatsID
    let cpuPercent: Float?
    let memoryBytes: UInt64
    let diskRwBytes: UInt64?
    
    var listChildren: [any AKListItem]? { nil }
    var textLabel: String? { nil }
}

private let refreshInterval = 1.0

struct ActivityMonitorRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    private let timer = Timer.publish(every: refreshInterval, on: .main, in: .common).autoconnect()

    @StateObject private var model = ActivityMonitorViewModel()
    @State private var selection: StatsID?

    var body: some View {
        StateWrapperView {
            AKList(AKSection.single(model.items), selection: $selection, rowHeight: 24, columns: [
                akColumn(id: "id", title: "ID") { item in
                    if case let .cgroupPath(id) = item.id {
                        Text(id)
                    }
                },
                akColumn(id: "cpuPercent", title: "CPU %") { item in
                    if let cpuPercent = item.cpuPercent {
                        Text(String(format: "%.1f", cpuPercent))
                    }
                },
                akColumn(id: "memoryBytes", title: "Memory") { item in
                    Text(ByteCountFormatter.string(fromByteCount: Int64(item.memoryBytes), countStyle: .memory))
                },
                akColumn(id: "diskRwBytes", title: "Disk I/O") { item in
                    if let diskRwBytes = item.diskRwBytes {
                        Text(ByteCountFormatter.string(fromByteCount: Int64(diskRwBytes), countStyle: .file))
                    }
                },
            ])
            .onAppear {
                Task { @MainActor in
                    await model.refresh(vmModel: vmModel)
                }
            }
            .onReceive(timer) { _ in
                Task { @MainActor in
                    await model.refresh(vmModel: vmModel)
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

    func refresh(vmModel: VmViewModel) async {
        var newStats: StatsResponse!
        do {
            newStats = try await vmModel.tryGetStats(GetStatsRequest(includeProcessCgPaths: []))
        } catch {
            return
        }
        let newEntries = Dictionary(uniqueKeysWithValues: newStats.entries.map { ($0.id, $0) })

        items = newEntries.map {
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
        }.sorted {
            if let lhsCpuPercent = $0.cpuPercent, let rhsCpuPercent = $1.cpuPercent {
                return lhsCpuPercent > rhsCpuPercent
            } else {
                return $0.id > $1.id
            }
        }

        lastEntries = newEntries
    }
}
