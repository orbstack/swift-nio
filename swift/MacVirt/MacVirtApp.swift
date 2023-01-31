//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI
import Connect

struct ExampleAuthInterceptor: Interceptor {
    init(config: ProtocolClientConfig) {}

    func unaryFunction() -> UnaryFunction {
        return UnaryFunction(
            requestFunction: { request in
                if request.url.host != "demo.connect.build" {
                    return request
                }

                var headers = request.headers
                headers["Authorization"] = ["SOME_USER_TOKEN"]
                return HTTPRequest(
                    url: request.url,
                    contentType: request.contentType,
                    headers: headers,
                    message: request.message
                )
            },
            responseFunction: { $0 } // Return the response as-is
        )
    }

    func streamFunction() -> StreamFunction {
        return StreamFunction(...)
    }
}

@main
struct MacVirtApp: App {
    init() {
        // spawn daemon
        let task = Process()
        task.launchPath = "/usr/local/bin/connectd"
        task.arguments = ["--config", "/Users/dannylin/.connect/config.yaml"]
        task.launch()
    }

    @State private var client = ProtocolClient(
        httpClient: URLSessionHTTPClient(),
        config: ProtocolClientConfig(
            host: "https://demo.connect.build",
            networkProtocol: .connect, // Or .grpcWeb
            codec: ProtoCodec(), // Or JSONCodec()
            
        )
    )

    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}
