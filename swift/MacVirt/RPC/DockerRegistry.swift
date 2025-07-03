import Foundation

struct DRRepository: Codable, Identifiable, Hashable {
    let name: String
    let namespace: String
    let repositoryType: String?
    let status: Int?
    let statusDescription: String?
    let description: String
    let isPrivate: Bool?
    let starCount: Int64?
    let pullCount: Int64?
    // let lastUpdated: Date
    // let lastModified: Date
    // let dateRegistered: Date
    let affiliation: String?
    let mediaTypes: [String]?
    let contentTypes: [String]?
    let categories: [DRCategory]?
    let storageSize: Int64?

    var id: String {
        "\(namespace)/\(name)"
    }
}

struct DRRepositoryList: Codable {
    let count: Int
    let next: String?
    let previous: String?
    let results: [DRRepository]
}

struct DRCategory: Codable, Hashable {
    let name: String
    let slug: String
}
