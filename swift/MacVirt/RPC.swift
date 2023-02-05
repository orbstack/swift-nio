//
//  RPC.swift
//  MacVirt
//
//  Created by Danny Lin on 1/31/23.
//

import Foundation
import SwiftJSONRPC


class SconService: RPCService {
    func ping() async throws {
        try await invoke("Ping")
    }
    
    func create()
}
