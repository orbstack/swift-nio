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
    case killswitchExpired
    case startTimeout(lastError: Error?)
    case stopError(error: Error)
    case setupError(error: Error)
    case dockerError(error: Error)
    case configRefresh(error: Error)
    case configPatchError(error: Error)

    // scon
    case startError(error: Error)
    case listRefresh(error: Error)
    case defaultError(error: Error)
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
        case .killswitchExpired:
            return "Build expired"
        case .startTimeout(let lastError):
            return "VM did not start: \(lastError?.localizedDescription ?? "timeout")"
        case .stopError(let error):
            return "Failed to stop VM: \(fmtRpc(error))"
        case .setupError(let error):
            return "Failed to do setup: \(fmtRpc(error))"
        case .dockerError(let error):
            return "Failed to check Docker: \(fmtRpc(error))"
        case .configRefresh(let error):
            return "Failed to get settings: \(fmtRpc(error))"
        case .configPatchError(let error):
            return "Failed to update settings: \(fmtRpc(error))"

        case .startError(let error):
            return "Failed to start machine manager: \(fmtRpc(error))"
        case .listRefresh(let error):
            return "Failed to get machines: \(fmtRpc(error))"
        case .defaultError(let error):
            return "Failed to set default machine: \(fmtRpc(error))"
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
            return "Please download the Apple Silicon version of OrbStack."
        case .killswitchExpired:
            return "Please download the latest version of OrbStack."
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
    case InvocationError.applicationError(let cause):
        switch cause {
        case let httpError as HTTPRequestExecutorError:
            switch httpError.reason {
            case .httpClientError(let clientError):
                return clientError.localizedDescription
            case .httpRequestError(let requestError):
                return requestError.localizedDescription
            case .httpResponseError(let responseError):
                return responseError.localizedDescription
            }
        default:
            return "Unknown error: \(cause)"
        }
    default:
        return error.localizedDescription
    }
}

struct ProfileChangedAlert {
    let profileRelPath: String
}

struct AddPathsAlert {
    let paths: [String]
}

class VmViewModel: ObservableObject {
    private let vmgr = VmService(client: newRPCClient("http://127.0.0.1:62420"))
    private let scon = SconService(client: newRPCClient("http://127.0.0.1:62421"))

    @Published private(set) var state = VmState.stopped
    @Published private(set) var containers: [ContainerRecord]?
    @Published private(set) var error: VmError?
    @Published var creatingCount = 0
    @Published private(set) var config: VmConfig?
    private(set) var reachedRunning = false

    @Published var presentProfileChanged: ProfileChangedAlert?
    @Published var presentAddPaths: AddPathsAlert?
    @Published var presentCreate = false

    // Docker
    @Published var dockerContainers: [DockerContainer]?
    
    // Setup
    @Published private(set) var isSshConfigWritable = true

    private func updateState(_ state: VmState) {
        self.state = state
        if state == .running {
            reachedRunning = true
        }
    }

    private func spawnDaemon() throws {
        guard state == .stopped else {
            return
        }

        guard !processIsTranslated() else {
            throw VmError.wrongArch
        }

        guard !killswitchExpired() else {
            throw VmError.killswitchExpired
        }

        Task {
            do {
                try await runProcessChecked(AppConfig.c.vmgrExe, ["spawn-daemon"])
                DispatchQueue.main.async {
                    self.updateState(.starting)
                }
            } catch let processError as ProcessError {
                DispatchQueue.main.async {
                    self.updateState(.stopped)
                    self.error = VmError.spawnExit(status: processError.status, output: processError.output)
                }
            } catch {
                DispatchQueue.main.async {
                    self.updateState(.stopped)
                    self.error = VmError.spawnError(error: error)
                }
            }
        }
        updateState(.spawning)
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
                state = .stopped
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
                state = .stopped
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

    @MainActor
    func refreshDockerList() async throws {
        guard state < .stopping else {
            return
        }

        // it's vmgr but need to wait for scon
        try await waitForScon()
        dockerContainers = try await vmgr.listDockerContainers()
    }

    @MainActor
    func tryRefreshDockerList() async {
        do {
            try await refreshDockerList()
        } catch {
            self.error = VmError.dockerError(error: error)
        }
    }

    @MainActor
    func refreshConfig() async throws {
        try await waitForVM()
        config = try await vmgr.getConfig()
    }

    @MainActor
    func tryRefreshConfig() async {
        do {
            try await refreshConfig()
        } catch {
            self.error = VmError.configRefresh(error: error)
        }
    }

    @MainActor
    func doSetup() async throws {
        let info = try await vmgr.startSetup()

        var waitTasks = [Task<Void, Error>]()

        if let pathCmd = info.alertProfileChanged {
            presentProfileChanged = ProfileChangedAlert(profileRelPath: pathCmd)
            waitTasks.append(Task {
                for await _ in $presentProfileChanged.first(where: { $0 == nil }).values {
                    break
                }
            })
        }

        if let paths = info.alertRequestAddPaths {
            presentAddPaths = AddPathsAlert(paths: paths)
            waitTasks.append(Task {
                for await _ in $presentAddPaths.first(where: { $0 == nil }).values {
                    break
                }
            })
        }

        // need to do anything?
        if let cmd = info.adminShellCommand {
            let reason = info.adminMessage ?? "make changes"
            let prompt = "\(Constants.userAppName) wants to \(reason)."
            waitTasks.append(Task.detached {
                do {
                    try runAsAdmin(script: cmd, prompt: prompt)
                } catch {
                    print("setup admin error: \(error)")
                }
            })
        }

        // wait for all tasks
        for task in waitTasks {
            try await task.value
        }

        // ok we're done
        try await vmgr.finishSetup()
    }

    @MainActor
    func initLaunch() async {
        await start()

        // do setup
        do {
            try await doSetup()
        } catch {
            self.error = VmError.setupError(error: error)
        }
    }

    @MainActor
    func start() async {
        do {
            try spawnDaemon()
        } catch VmError.wrongArch {
            self.error = VmError.wrongArch
        } catch VmError.killswitchExpired {
            self.error = VmError.killswitchExpired
        } catch {
            self.error = VmError.spawnError(error: error)
            return
        }

        // this includes wait
        print("try refresh: start")
        await tryRefreshList()
        await tryRefreshConfig()
        updateState(.running)
    }

    @MainActor
    func stop() async {
        updateState(.stopping)
        do {
            try await vmgr.stop()
        } catch {
            self.error = VmError.stopError(error: error)
        }
        updateState(.stopped)
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
        print("try create one \(name) \(distro) \(arch)")
        do {
            try await createContainer(name: name, distro: distro, arch: arch)
        } catch {
            self.error = VmError.containerCreateError(error: error)
        }
    }

    func patchConfig(_ patch: VmConfig) async throws {
        try await vmgr.patchConfig(patch)
        try await refreshConfig()
    }

    @MainActor
    func tryPatchConfig(_ patch: VmConfig) async {
        do {
            try await patchConfig(patch)
        } catch {
            self.error = VmError.configPatchError(error: error)
        }
    }

    @MainActor
    func trySetDefaultContainer(_ record: ContainerRecord) async {
        do {
            try await scon.setDefaultContainer(record)
        } catch {
            self.error = VmError.defaultError(error: error)
        }
    }

    @MainActor
    func tryRefreshSshConfigWritable() async {
        do {
            isSshConfigWritable = try await vmgr.isSshConfigWritable()
        } catch {
            print("ssh config writable error: \(error)")
        }
    }

    func dismissError() {
        error = nil

        // refresh in case e.g. container was deleted after create failed
        Task {
            do {
                try await refreshList()
            } catch {}
        }
    }
}
