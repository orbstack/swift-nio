import AsyncHTTPClient
import Foundation
import NIO
import NIOHTTP1
import _NIOFileSystem

private let responseBodyLimit: Int = 4 * 1024 * 1024  // 4 MiB
private let chunkSize: ByteCount = .megabytes(1)

// retry: exponential backoff up to 5 sec
private let connectTimeout: TimeAmount = .seconds(5)
// for long downloads/machine creation
private let readTimeout: TimeAmount = .minutes(30)

extension URL {
    var filePath: FilePath {
        FilePath(self.path)
    }
}

enum DockerError: LocalizedError {
    case api(status: HTTPResponseStatus, message: String?)

    case decode(cause: Error)
    case encode(cause: Error)
    case request(cause: Error)
    case readResponse(cause: Error)

    case eof

    // creation errors
    case invalidURL

    var errorDescription: String? {
        switch self {
        case let .api(status, message):
            return "\(message ?? "unknown error") (\(status))"
        case let .decode(cause):
            return "Decode error: \(cause)"
        case let .encode(cause):
            return "Encode error: \(cause)"
        case let .request(cause):
            return "Request error: \(cause)"
        case let .readResponse(cause):
            return "Read response error: \(cause)"
        case .eof:
            return "Connection lost"
        case .invalidURL:
            return "Invalid URL"
        }
    }
}

class DockerClient {
    private let client: HTTPClient
    private let baseURL: URL

    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    init(unixSocket: String) throws {
        var config = HTTPClient.Configuration()
        // this is a unix socket, waiting for internet won't help
        config.networkFrameworkWaitForConnectivity = false
        // fix error when stopping many containers concurrently, if they take a while to stop
        config.connectionPool.concurrentHTTP1ConnectionsPerHostSoftLimit = 32
        config.timeout.connect = connectTimeout
        config.timeout.read = readTimeout
        client = HTTPClient(configuration: config)

        guard let url = URL(httpURLWithSocketPath: unixSocket, uri: "/") else {
            throw DockerError.invalidURL
        }
        baseURL = url

        //encoder.keyEncodingStrategy = .convertToSnakeCase
        decoder.dateDecodingStrategy = .iso8601
        //decoder.keyDecodingStrategy = .convertFromSnakeCase
    }

    // MARK: - helper

    private func callRaw(
        _ method: HTTPMethod, _ pathComponents: [String], query: [URLQueryItem] = [],
        contentType: String = "application/json", acceptType: String = "application/json",
        body: HTTPClientRequest.Body? = nil
    ) async throws -> HTTPClientResponse.Body {
        var url = baseURL
        for component in pathComponents {
            url.appendPathComponent(component)
        }
        url.append(queryItems: query)

        var req = HTTPClientRequest(url: url.absoluteString)
        req.method = method
        req.headers.add(name: "Host", value: "docker")
        req.headers.add(name: "Content-Type", value: contentType)
        req.headers.add(name: "Accept", value: acceptType)
        req.headers.add(name: "User-Agent", value: "OrbStack-GUI")
        req.body = body

        var response: HTTPClientResponse
        do {
            response = try await client.execute(req, timeout: readTimeout)
        } catch let e as HTTPClientError where e == .remoteConnectionClosed {
            throw DockerError.eof
        } catch {
            throw DockerError.request(cause: error)
        }

        // check HTTP status
        if (response.status.code < 200 || response.status.code >= 300)
            && response.status != .notModified
        {
            // read error
            let err = try await response.body.collect(upTo: responseBodyLimit)
            let errJson = try? decoder.decode(DKAPIError.self, from: err)
            throw DockerError.api(status: response.status, message: errJson?.message)
        }

        return response.body
    }

    private func call<Response: Decodable>(
        _ method: HTTPMethod, _ pathComponents: [String], query: [URLQueryItem] = [],
        body: HTTPClientRequest.Body? = nil
    ) async throws -> Response {
        let body = try await callRaw(method, pathComponents, query: query, body: body)
        let data = try await body.collect(upTo: responseBodyLimit)
        return try decoder.decode(Response.self, from: data)
    }

    private func call(
        _ method: HTTPMethod, _ pathComponents: [String], query: [URLQueryItem] = [],
        body: HTTPClientRequest.Body? = nil
    ) async throws {
        let body = try await callRaw(method, pathComponents, query: query, body: body)
        _ = try await body.collect(upTo: responseBodyLimit)
    }

    private func call<Request: Encodable, Response: Decodable>(
        _ method: HTTPMethod, _ pathComponents: [String], query: [URLQueryItem] = [], body: Request
    ) async throws -> Response {
        let body = try encoder.encode(body)
        return try await call(method, pathComponents, query: query, body: .bytes(body))
    }

    private func call<Request: Encodable>(
        _ method: HTTPMethod, _ pathComponents: [String], query: [URLQueryItem] = [], body: Request
    ) async throws {
        let body = try encoder.encode(body)
        try await call(method, pathComponents, query: query, body: .bytes(body))
    }

    // MARK: - ctr

    func containerList(all: Bool = false) async throws -> [DKContainer] {
        var query = [URLQueryItem]()
        if all {
            query.append(URLQueryItem(name: "all", value: "true"))
        }
        return try await call(.GET, ["containers", "json"], query: query)
    }

    func containerStart(id: String) async throws {
        try await call(.POST, ["containers", id, "start"])
    }

    func containerStop(id: String) async throws {
        try await call(.POST, ["containers", id, "stop"])
    }

    func containerKill(id: String) async throws {
        try await call(.POST, ["containers", id, "kill"])
    }

    func containerRestart(id: String) async throws {
        try await call(.POST, ["containers", id, "restart"])
    }

    func containerPause(id: String) async throws {
        try await call(.POST, ["containers", id, "pause"])
    }

    func containerUnpause(id: String) async throws {
        try await call(.POST, ["containers", id, "unpause"])
    }

    func containerDelete(id: String, force: Bool = true) async throws {
        var query = [URLQueryItem]()
        if force {
            query.append(URLQueryItem(name: "force", value: "true"))
        }
        try await call(.DELETE, ["containers", id], query: query)
    }

    // MARK: - volume

    func volumeList() async throws -> DKVolumeListResponse {
        try await call(.GET, ["volumes"])
    }

    func volumeCreate(options: DKVolumeCreateOptions) async throws -> DKVolume {
        return try await call(.POST, ["volumes", "create"], body: options)
    }

    func volumeDelete(id: String) async throws {
        try await call(.DELETE, ["volumes", id])
    }

    // MARK: - image

    func imageList() async throws -> [DKImage] {
        let query = [URLQueryItem(name: "shared-size", value: "1")]
        return try await call(.GET, ["images", "json"], query: query)
    }

    func imageInspect(id: String) async throws -> DKFullImage {
        try await call(.GET, ["images", id, "json"])
    }

    func imageDelete(id: String, force: Bool = true) async throws {
        var query = [URLQueryItem]()
        if force {
            query.append(URLQueryItem(name: "force", value: "true"))
        }
        try await call(.DELETE, ["images", id], query: query)
    }

    func imageImport(url: URL) async throws {
        // Foundation's FileHandle.bytes is byte-by-byte, so *REALLY* slow (minutes to import 200MB). NIO has a better implementation.
        try await FileSystem.shared.withFileHandle(forReadingAt: url.filePath) { file in
            let info = try await file.info()
            let body = HTTPClientRequest.Body.stream(
                file.readChunks(chunkLength: chunkSize), length: .known(info.size))
            _ = try await callRaw(
                .POST, ["images", "load"], query: [URLQueryItem(name: "quiet", value: "true")],
                contentType: "application/x-tar", body: body)
        }
    }

    func imageExport(id: String, url: URL) async throws {
        let body = try await callRaw(.GET, ["images", id, "get"], acceptType: "application/x-tar")

        // do writes in background
        try await Task {
            FileManager.default.createFile(atPath: url.path, contents: nil, attributes: nil)
            let file = try FileHandle(forWritingTo: url)
            defer { try? file.close() }

            for try await chunk in body {
                file.write(Data(buffer: chunk, byteTransferStrategy: .noCopy))
            }
        }.value
    }

    // MARK: - net

    func networkList() async throws -> [DKNetwork] {
        try await call(.GET, ["networks"])
    }

    func networkCreate(options: DKNetwork) async throws -> DKNetworkCreateResponse {
        return try await call(.POST, ["networks", "create"], body: options)
    }

    func networkDelete(id: String) async throws {
        try await call(.DELETE, ["networks", id])
    }
}
