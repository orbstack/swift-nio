//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import SwiftJSONRPC

enum VmState {
    case stopped
    case spawning
    case starting
    case running
    case stopping
}

private let startTimeout = 15 * 1000 * 1000 * 1000

enum VmError: Error {
    case spawnExit(status: Int32, output: String)
    case wrongArch
    case startTimeout
}

enum SconError: Error {
    case startTimeout
}

class VmViewModel: ObservableObject {
    private let vmgr = VmService(client: newRPCClient("http://127.0.0.1:62420"))
    private let scon = SconService(client: newRPCClient("http://127.0.0.1:62421"))
    private let daemon = DaemonManager()

    @Published var state = VmState.stopped
    @Published var containers: [ContainerRecord]?
    @Published var error: Error?

    func earlyInit() {
        do {
            try spawnDaemon()
        } catch {
            print("Failed to spawn daemon (early): \(error)")
        }
    }

    private func spawnDaemon() throws {
        if state != .stopped {
            return
        }

        if processIsTranslated() {
            throw VmError.wrongArch
        }

        let task = Process()
        if let path = AppConfig.c.vmgrExePath {
            task.launchPath = path
        } else {
            task.launchPath = Bundle.main.path(forResource: "bin/macvmgr", ofType: "")
        }

        let outPipe = Pipe()
        task.standardOutput = outPipe
        task.standardError = outPipe
        task.arguments = ["spawn-daemon"]
        task.terminationHandler = { process in
            if process.terminationStatus != 0 {
                print("Daemon exited with status \(process.terminationStatus)")
                let output = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)!
                DispatchQueue.main.async {
                    self.state = .stopped
                    self.error = VmError.spawnExit(status: process.terminationStatus, output: output)
                }
            } else {
                DispatchQueue.main.async {
                    self.state = .starting
                }
            }
        }
        state = .spawning
        try task.run()
    }

    private func waitForVM() async throws {
        let deadline = DispatchTime.now() + .nanoseconds(startTimeout)
        while true {
            do {
                try await vmgr.ping()
                break
            } catch {
                print("Failed to ping VM: \(error)")
            }
            try await Task.sleep(nanoseconds: 100 * 1000 * 1000)
            if (DispatchTime.now() > deadline) {
                throw VmError.startTimeout
            }
        }
    }

    private func waitForScon() async throws {
        try await waitForVM()

        let deadline = DispatchTime.now() + .nanoseconds(startTimeout)
        while true {
            do {
                try await scon.ping()
                break
            } catch {
                print("Failed to ping Scon: \(error)")
            }
            try await Task.sleep(nanoseconds: 100 * 1000 * 1000)
            if (DispatchTime.now() > deadline) {
                throw SconError.startTimeout
            }
        }
    }

    @MainActor
    func refreshList() async throws {
        try await waitForScon()
        let allContainers = try await scon.listContainers()
        // filter into running and stopped
        let runningContainers = allContainers.filter { $0.running }
        let stoppedContainers = allContainers.filter { !$0.running }
        // sort alphabetically by name within each group
        containers = runningContainers.sorted { $0.name < $1.name } +
                stoppedContainers.sorted { $0.name < $1.name }
        print("Refreshed list: \(allContainers)")
    }

    func tryRefreshList() async {
        do {
            try await refreshList()
        } catch {
            print("Failed to refresh list: \(error)")
        }
    }

    func initLaunch() async {
        await start()
    }

    func start() async {
        do {
            try spawnDaemon()
        } catch {
            print("Failed to init launch: \(error)")
        }

        do {
            try await waitForScon()
        } catch {
            print("Failed to wait for Scon: \(error)")
        }

        do {
            try await refreshList()
        } catch {
            print("Failed to refresh list: \(error)")
        }

        state = .running
    }

    @MainActor
    func stop() async {
        state = .stopping
        do {
            try await vmgr.stop()
        } catch {
            print("Failed to stop daemon: \(error)")
        }
        state = .stopped
    }

    func stopContainer(_ record: ContainerRecord) async throws {
        try await scon.containerStop(record)
        try await refreshList()
    }

    func startContainer(_ record: ContainerRecord) async throws {
        try await scon.containerStart(record)
        try await refreshList()
    }

    func deleteContainer(_ record: ContainerRecord) async throws {
        try await scon.containerDelete(record)
        try await refreshList()
    }

    func createContainer(name: String, distro: Distro, arch: String) async throws {
        try await scon.create(name: name, image: ImageSpec(
            distro: distro.imageKey.rawValue,
                version: "latest",
            arch: arch,
            variant: ""
        ), userPassword: nil)
        try await refreshList()
    }
}