//
//  Daemon.swift
//  MacVirt
//
//  Created by Danny Lin on 1/31/23.
//

import Foundation

enum DaemonState {
    case updating, starting, running
}

class DaemonManager: ObservableObject {
    private var spawned = false

    @Published var state = DaemonState.updating

    func spawn() throws {
        if (spawned) {
            return
        }

        let task = Process()
        if let path = AppConfig.c.vmgrExePath {
            task.launchPath = path
        } else {
            task.launchPath = Bundle.main.path(forResource: "bin/macvmgr", ofType: "")
        }

        task.arguments = ["spawn-daemon"]
        task.terminationHandler = { process in
            self.state = .starting
        }
        try task.run()
        
        spawned = true
    }
}
