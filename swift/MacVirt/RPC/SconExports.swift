struct ExportedMachineV1: Codable {
    var version: Int
    var record: ContainerRecord
    // var exportedAt: Date
    // var hostUid: UInt32
    // var hostGid: UInt32
    // var sourceFs: String
    // var subvolumes: [ExportedMachineSubvolume]
}

struct ExportedVolumeConfigV1: Codable {
    // var version: Int

    var name: String
    var createdAt: String

    var labels: [String: String]?
    var options: [String: String]?
}
