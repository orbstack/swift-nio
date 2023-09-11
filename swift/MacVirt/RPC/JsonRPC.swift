//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import NIO
import AsyncHTTPClient
import Atomics

// retry: exponential backoff up to 5 sec
private let connectTimeout: TimeAmount = .seconds(5)
// for long downloads/machine creation
private let readTimeout: TimeAmount = .minutes(30)

enum RPCError: LocalizedError {
    case httpStatus(Int)
    case decode(cause: Error)
    case encode(cause: Error)
    case request(cause: Error)
    case readResponse(cause: Error)

    case app(code: Int, message: String)
    case noResult

    case eof

    var errorDescription: String? {
        switch self {
        case .httpStatus(let status):
            return "HTTP status \(status)"
        case .decode(let cause):
            return "Decode error: \(cause)"
        case .encode(let cause):
            return "Encode error: \(cause)"
        case .request(let cause):
            return "Request error: \(cause)"
        case .readResponse(let cause):
            return "Read response error: \(cause)"

        case .app(_, let message):
            return message
        case .noResult:
            return "No result"

        case .eof:
            return "Connection lost"
        }
    }
}

private struct None: Codable {}

private struct JsonRPCRequest<Params: Codable>: Codable {
    var jsonrpc: String
    var id: Int
    var method: String
    var params: Params?
}

private struct JsonRPCResponse<Result: Codable>: Codable {
    var jsonrpc: String
    var id: Int
    var result: Result?
    var error: JsonRPCError?
}

private struct JsonRPCError: Codable {
    var code: Int
    var message: String
    var data: String?
}

// simple JSONRPC client using SwiftNIO AsyncHTTPClient for Unix socket support
class JsonRPCClient {
    private let client: HTTPClient
    private let baseURL: String

    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    private let reqId = ManagedAtomic(1)

    init(unixSocket: String) {
        var config = HTTPClient.Configuration()
        // this is a unix socket, waiting for internet won't help
        config.networkFrameworkWaitForConnectivity = false
        // fix error when stopping many containers concurrently, if they take a while to stop
        config.connectionPool.concurrentHTTP1ConnectionsPerHostSoftLimit = 32
        config.timeout.connect = connectTimeout
        config.timeout.read = readTimeout
        client = HTTPClient(configuration: config)

        self.baseURL = URL(httpURLWithSocketPath: unixSocket, uri: "/")!.absoluteString

        encoder.keyEncodingStrategy = .convertToSnakeCase
        decoder.keyDecodingStrategy = .convertFromSnakeCase
    }

    private func nextReqId() -> Int {
        return reqId.loadThenWrappingIncrement(ordering: .relaxed)
    }

    func call(_ method: String) async throws {
        do {
            let _: None = try await call(method, args: nil as None?)
        } catch RPCError.noResult {
            // ignore
        }
    }

    func call<Result: Codable>(_ method: String) async throws -> Result {
        return try await call(method, args: nil as None?)
    }

    func call<Args: Codable>(_ method: String, args: Args) async throws {
        do {
            let _: None = try await call(method, args: args)
        } catch RPCError.noResult {
            // ignore
        }
    }

    func call<Args: Codable, Result: Codable>(_ method: String, args: Args? = nil) async throws -> Result {
        var req = HTTPClientRequest(url: baseURL)
        req.method = .POST
        // Go refuses reqs with unix socket as host
        req.headers.add(name: "Host", value: "rpc")
        req.headers.add(name: "Content-Type", value: "application/json")
        req.headers.add(name: "Accept", value: "application/json")
        req.headers.add(name: "User-Agent", value: "OrbStack-GUI")
        do {
            let body = try encoder.encode(JsonRPCRequest(jsonrpc: "2.0",
                    id: nextReqId(),
                    method: method,
                    params: args))
            req.body = .bytes(body)
        } catch {
            throw RPCError.encode(cause: error)
        }

        var response: HTTPClientResponse
        do {
            response = try await client.execute(req, timeout: readTimeout)
        } catch let e as HTTPClientError where e == .remoteConnectionClosed {
            throw RPCError.eof
        } catch {
            throw RPCError.request(cause: error)
        }

        // check HTTP status
        if response.status.code != 200 {
            throw RPCError.httpStatus(Int(response.status.code))
        }

        var respData: ByteBuffer
        do {
            respData = try await response.body.collect(upTo: 1024*1024) // 1 MiB
        } catch let e as HTTPClientError where e == .remoteConnectionClosed {
            throw RPCError.eof
        } catch {
            throw RPCError.readResponse(cause: error)
        }

        var respJson: JsonRPCResponse<Result>
        do {
            respJson = try decoder.decode(JsonRPCResponse<Result>.self, from: Data(buffer: respData))
        } catch {
            throw RPCError.decode(cause: error)
        }

        if let error = respJson.error {
            throw RPCError.app(code: error.code, message: error.message)
        }

        if let result = respJson.result {
            return result
        } else {
            throw RPCError.noResult
        }
    }
}
