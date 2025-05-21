import Foundation

// zstd skippable frame: magic 0x184D2A5C, then 4-byte little-endian size, then data
private let zstdSkippableFrameMagic: Data = Data([0x5c, 0x2a, 0x4d, 0x18])

// orbstack magic (little-endian): 07 b5 1a cc ("orbstack")
// our zstd frame data consists of: orbstack magic (little-endian), orbstack version (little-endian, 4 bytes), data
private let orbMagic: Data = Data([0xcc, 0x1a, 0xb5, 0x07])

// max size to prevent DoS
private let maxSkippableFrameSize = 32 * 1024 * 1024 // 32 MiB

enum Zstd {
    static func readSkippableFrame<T: Decodable>(file: FileHandle, expectedVersion: UInt32) throws -> T {
        guard let header = try file.read(upToCount: 4 + 4 + 4 + 4) else {
            throw ZstdError.emptyRead
        }
        if header.count != 4 + 4 + 4 + 4 {
            throw ZstdError.shortRead
        }

        let magic = header[0..<4]
        if magic != zstdSkippableFrameMagic {
            throw ZstdError.invalidFrameMagic
        }

        let sizeBytes = header[4..<8]
        let size = UInt32(littleEndian: sizeBytes.withUnsafeBytes { $0.load(as: UInt32.self) })
        if size < 8 || size > maxSkippableFrameSize {
            throw ZstdError.frameTooLarge
        }

        let orbMagic = header[8..<12]
        if orbMagic != orbMagic {
            throw ZstdError.invalidOrbMagic
        }

        let versionBytes = header[12..<16]
        let version = UInt32(littleEndian: versionBytes.withUnsafeBytes { $0.load(as: UInt32.self) })
        if version != expectedVersion {
            throw ZstdError.invalidFrameVersion
        }

        let payloadSize = Int(size - 8)
        guard let data = try file.read(upToCount: payloadSize) else {
            throw ZstdError.emptyRead
        }
        if data.count != payloadSize {
            throw ZstdError.shortRead
        }

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        decoder.keyDecodingStrategy = .convertFromSnakeCase

        return try decoder.decode(T.self, from: data)
    }

    static func readSkippableFrame<T: Decodable>(url: URL, expectedVersion: UInt32) throws -> T {
        let file = try FileHandle(forReadingFrom: url)
        defer { file.closeFile() }
        return try readSkippableFrame(file: file, expectedVersion: expectedVersion)
    }
}

enum ZstdFrameVersion {
    static let machineConfig1: UInt32 = (0 << 16) | 1
    static let dockerVolumeConfig1: UInt32 = (1 << 16) | 1
}

enum ZstdError: Error {
    case emptyRead
    case shortRead
    case invalidFrameMagic
    case frameTooLarge
    case invalidFrameVersion
    case invalidOrbMagic
}
