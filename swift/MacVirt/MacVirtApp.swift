//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import SwiftUI
import Connect

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
            codec: ProtoCodec() // Or JSONCodec()
        )
    )

    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}
