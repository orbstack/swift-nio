//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import SwiftJSONRPC

enum VmState: Int, Comparable {
    case stopped
    case spawning
    case starting
    case running
    case stopping

    static func <(lhs: VmState, rhs: VmState) -> Bool {
        lhs.rawValue < rhs.rawValue
    }
}

private let startTimeout = 15 * 1000 * 1000 * 1000

enum VmError: LocalizedError, Equatable {
    // VM
    case spawnError(error: Error)
    case spawnExit(status: Int32, output: String)
    case wrongArch
    case startTimeout(lastError: Error?)
    case stopError(error: Error)

    // scon
    case startError(error: Error)
    case listRefresh(error: Error)
    case containerStopError(error: Error)
    case containerStartError(error: Error)
    case containerDeleteError(error: Error)
    case containerCreateError(error: Error)

    var errorDescription: String? {
        switch self {
        case .spawnError(let error):
            return "Failed to start VM: \(error.localizedDescription)"
        case .spawnExit(let status, let output):
            return "VM crashed with status \(status): \(output)"
        case .wrongArch:
            return "Wrong CPU type"
        case .startTimeout(let lastError):
            return "VM did not start: \(lastError?.localizedDescription ?? "timeout")"
        case .stopError(let error):
            return "Failed to stop VM: \(fmtRpc(error))"

        case .startError(let error):
            return "Failed to start machine manager: \(fmtRpc(error))"
        case .listRefresh(let error):
            return "Failed to get machines: \(fmtRpc(error))"
        case .containerStopError(let error):
            return "Failed to stop machine: \(fmtRpc(error))"
        case .containerStartError(let error):
            return "Failed to start machine: \(fmtRpc(error))"
        case .containerDeleteError(let error):
            return "Failed to delete machine: \(fmtRpc(error))"
        case .containerCreateError(let error):
            return "Failed to create machine: \(fmtRpc(error))"
        }
    }

    var recoverySuggestion: String? {
        switch self {
        case .spawnError:
            return "Check the log for more details."
        case .spawnExit:
            return "Check the log for more details."
        case .wrongArch:
            return "Please download the Apple Silicon version of this app."
        case .startTimeout:
            return "Check the log for more details."
        case .stopError:
            return "Check the log for more details."

        case .startError:
            return "Check the log for more details."

        default:
            return nil
        }
    }

    static func ==(lhs: VmError, rhs: VmError) -> Bool {
        lhs.errorDescription == rhs.errorDescription
    }
}

private func fmtRpc(_ error: Error) -> String {
    switch error {
    case InvocationError.rpcError(let rpcError):
        return rpcError.message
    default:
        return error.localizedDescription
    }
}

class VmViewModel: ObservableObject {
    private let vmgr = VmService(client: newRPCClient("http://127.0.0.1:62420"))
    private let scon = SconService(client: newRPCClient("http://127.0.0.1:62421"))
    private let daemon = DaemonManager()

    @Published private(set) var state = VmState.stopped
    @Published private(set) var containers: [ContainerRecord]?
    @Published var error: VmError?
    @Published var creatingCount = 0

    func earlyInit() {
        do {
            try spawnDaemon()
        } catch {
            self.error = VmError.spawnError(error: error)
        }
    }

    private func spawnDaemon() throws {
        guard state == .stopped else {
            return
        }

        guard !processIsTranslated() else {
            throw VmError.wrongArch
        }

        let exePath: String
        if let path = AppConfig.c.vmgrExePath {
            exePath = path
        } else {
            exePath = Bundle.main.path(forResource: "bin/macvmgr", ofType: nil)!
        }

        Task {
            do {
                try await runProcessChecked(exePath, ["spawn-daemon"])
                DispatchQueue.main.async {
                    self.state = .starting
                }
            } catch let processError as ProcessError {
                DispatchQueue.main.async {
                    self.state = .stopped
                    self.error = VmError.spawnExit(status: processError.status, output: processError.output)
                }
            } catch {
                DispatchQueue.main.async {
                    self.state = .stopped
                    self.error = VmError.spawnError(error: error)
                }
            }
        }
        state = .spawning
    }

    private func waitForVM() async throws {
        // wait for .starting
        for await value in $state.first(where: { $0 >= .starting }).values {
            if value == .starting {
                break
            }
        }

        let deadline = DispatchTime.now() + .nanoseconds(startTimeout)
        var lastError: Error?
        while true {
            do {
                try await vmgr.ping()
                break
            } catch {
                lastError = error
            }
            try await Task.sleep(nanoseconds: 100 * 1000 * 1000)
            if (DispatchTime.now() > deadline) {
                throw VmError.startTimeout(lastError: lastError)
            }
        }
    }

    private func waitForScon() async throws {
        guard state < .running else {
            return
        }

        try await waitForVM()

        let deadline = DispatchTime.now() + .nanoseconds(startTimeout)
        var lastError: Error?
        while true {
            do {
                try await scon.ping()
                break
            } catch {
                lastError = error
            }
            try await Task.sleep(nanoseconds: 100 * 1000 * 1000)
            if (DispatchTime.now() > deadline) {
                throw VmError.startTimeout(lastError: lastError)
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
    }

    @MainActor
    func tryRefreshList() async {
        do {
            try await refreshList()
        } catch {
            self.error = VmError.listRefresh(error: error)
        }
    }

    func initLaunch() async {
        await start()
    }

    @MainActor
    func start() async {
        do {
            try spawnDaemon()
        } catch {
            self.error = VmError.spawnError(error: error)
            return
        }

        // this includes wait
        await tryRefreshList()
        state = .running
    }

    @MainActor
    func stop() async {
        state = .stopping
        do {
            try await vmgr.stop()
        } catch {
            self.error = VmError.stopError(error: error)
        }
        state = .stopped
    }

    func stopContainer(_ record: ContainerRecord) async throws {
        try await scon.containerStop(record)
        try await refreshList()
    }

    @MainActor
    func tryStopContainer(_ record: ContainerRecord) async {
        do {
            try await stopContainer(record)
        } catch {
            self.error = VmError.containerStopError(error: error)
        }
    }

    func startContainer(_ record: ContainerRecord) async throws {
        try await scon.containerStart(record)
        try await refreshList()
    }

    @MainActor
    func tryStartContainer(_ record: ContainerRecord) async {
        do {
            try await startContainer(record)
        } catch {
            self.error = VmError.containerStartError(error: error)
        }
    }

    func deleteContainer(_ record: ContainerRecord) async throws {
        try await scon.containerDelete(record)
        try await refreshList()
    }

    @MainActor
    func tryDeleteContainer(_ record: ContainerRecord) async {
        do {
            try await deleteContainer(record)
        } catch {
            self.error = VmError.containerDeleteError(error: error)
        }
    }

    func createContainer(name: String, distro: Distro, arch: String) async throws {
        try await scon.create(name: name, image: ImageSpec(
            distro: distro.imageKey.rawValue,
            version: "",
            arch: arch,
            variant: ""
        ), userPassword: nil)
        try await refreshList()
    }

    @MainActor
    func tryCreateContainer(name: String, distro: Distro, arch: String) async {
        do {
            try await createContainer(name: name, distro: distro, arch: arch)
        } catch {
            self.error = VmError.containerCreateError(error: error)
        }
    }
}