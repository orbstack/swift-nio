//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftJSONRPC

private class GoRequestAdapter: HTTPRequestAdapter {
    func adapt(request: HTTPRequest) throws -> HTTPRequest {
        var request = request
        request.headers["Content-Type"] = "application/json"
        return request
    }
}

func newRPCClient(_ url: String) -> RPCClient {
    // TODO change client
    URLSession.shared.configuration.timeoutIntervalForRequest = 15 * 60
    URLSession.shared.configuration.timeoutIntervalForResource = 15 * 60

    let executor = HTTPRequestExecutor(config: HTTPRequestExecutorConfig(
        baseURL: URL(string: url)!,
        throttle: .disabled
    ))
    executor.requestAdapter = GoRequestAdapter()
    let client = RPCClient(requestExecutor: executor)
    return client
}