//
//  DockerSortMethod.swift
//  MacVirt
//
//  Created by Serena on 21/06/2024.
//  

import Foundation

enum DockerSortMethod: Int, CaseIterable, Hashable, CustomStringConvertible {
    case none // defaults to sorting name
    
    case size
    
    var description: String {
        switch self {
        case .none:
            return "Name"
        case .size:
            return "Size"
        }
    }
}

protocol DockerItemSortConforming {
    // The name used to sort this object
    @MainActor
    var sortName: String { get }
    
    @MainActor
    static func objectBiggerInSize(lhs: Self, rhs: Self, context: VmViewModel) -> Bool
}

extension Array where Element: DockerItemSortConforming {
    @MainActor
    func sort(accordingTo method: DockerSortMethod, model: VmViewModel) -> Self {
        switch method {
        case .none:
            return self.sorted { item1, item2 in
                item1.sortName < item2.sortName
            }
        case .size:
            return self.sorted { item1, item2 in
                return Element.objectBiggerInSize(lhs: item1, rhs: item2, context: model)
            }
        }
    }
}

extension DKContainer: DockerItemSortConforming {
    static func objectBiggerInSize(lhs container: DKContainer, rhs container2: DKContainer, context model: VmViewModel) -> Bool {
        fatalError("Not allowed.")
    }
    
    var sortName: String { self.userName }
}

extension DKVolume: DockerItemSortConforming {
    static func objectBiggerInSize(lhs image: DKVolume, rhs image2: DKVolume, context model: VmViewModel) -> Bool {
        return (image.size(model: model) ?? 0) > (image2.size(model: model) ?? 0)
    }
    
    var sortName: String { self.name }
    
    @MainActor
    func size(model: VmViewModel) -> Int64? {
        guard let dockerDf = model.dockerSystemDf,
              let dfVolume = dockerDf.volumes.first(where: { $0.name == self.name }),
              let usageData = dfVolume.usageData else { return nil }
        return usageData.size
    }
}

extension DKSummaryAndFullImage: DockerItemSortConforming {
    static func objectBiggerInSize(lhs image: DKSummaryAndFullImage, rhs image2: DKSummaryAndFullImage, context: VmViewModel) -> Bool {
        return image.summary.size > image2.summary.size
    }
    
    var sortName: String { self.summary.userTag }
}
