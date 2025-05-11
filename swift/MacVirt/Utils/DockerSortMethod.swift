//
//  DockerGenericSortDescriptor.swift
//  MacVirt
//
//  Created by Serena on 21/06/2024.
//

import Foundation
import Defaults

protocol DockerItemSortConforming {
    var sortName: String { get }
    var sortDate: Date { get }

    @MainActor
    func sortSize(context: VmViewModel) -> Int64?
}

extension Array where Element: DockerItemSortConforming {
    @MainActor
    mutating func sort(accordingTo method: DockerGenericSortDescriptor, model: VmViewModel) {
        switch method {
        case .name:
            self.sort { lhs, rhs in
                lhs.sortName < rhs.sortName
            }
        case .size:
            self.sort { lhs, rhs in
                (lhs.sortSize(context: model) ?? 0) > (rhs.sortSize(context: model) ?? 0)
            }
        case .dateDescending:
            self.sort { lhs, rhs in
                lhs.sortDate > rhs.sortDate
            }
        case .dateAscending:
            self.sort { lhs, rhs in
                lhs.sortDate < rhs.sortDate
            }
        }
    }
}

extension DKVolume: DockerItemSortConforming {
    var sortName: String { self.name }
    var sortDate: Date { self.createdAt ?? Date.distantPast }

    @MainActor
    func sortSize(context: VmViewModel) -> Int64? {
        guard let dockerDf = context.dockerSystemDf,
            let dfVolume = dockerDf.volumes.first(where: { $0.name == self.name }),
            let usageData = dfVolume.usageData
        else { return nil }
        return usageData.size
    }
}

extension DKSummaryAndFullImage: DockerItemSortConforming {
    var sortName: String { self.summary.userTag }
    var sortDate: Date { Date(timeIntervalSince1970: TimeInterval(self.summary.created)) }

    @MainActor
    func sortSize(context: VmViewModel) -> Int64? {
        return self.summary.size
    }
}
