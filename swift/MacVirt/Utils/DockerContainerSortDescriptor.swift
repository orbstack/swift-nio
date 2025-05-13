import Defaults
import Foundation

extension [DKContainer] {
    mutating func sort(accordingTo descriptor: DockerContainerSortDescriptor) {
        switch descriptor {
        case .name:
            self.sort(by: { $0.userName < $1.userName })
        case .dateDescending:
            self.sort(by: { $0.created > $1.created })
        case .dateAscending:
            self.sort(by: { $0.created < $1.created })
        case .image:
            self.sort(by: { $0.image < $1.image })
        }
    }
}
