//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import SwiftJSONRPC
import Sentry

fileprivate let startPollInterval: UInt64 = 100 * 1000 * 1000 // 100 ms

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

private let startTimeout = 3 * 60 * 1000 * 1000 * 1000 // 3 minutes

enum VmError: LocalizedError, Equatable {
    // VM
    case spawnError(cause: Error)
    case spawnExit(status: Int32, output: String)
    case wrongArch
    case killswitchExpired
    case startFailed(cause: Error?)
    case startTimeout(cause: Error?)
    case stopError(cause: Error)
    case setupError(cause: Error)
    case dockerError(cause: Error)
    case configRefresh(cause: Error)
    case configPatchError(cause: Error)

    // scon
    case startError(cause: Error)
    case listRefresh(cause: Error)
    case defaultError(cause: Error)
    case containerStopError(cause: Error)
    case containerStartError(cause: Error)
    case containerDeleteError(cause: Error)
    case containerCreateError(cause: Error)

    var errorDescription: String? {
        switch self {
        case .spawnError(let cause):
            return "Failed to start helper: \(cause.localizedDescription)"
        case .spawnExit(let status, let output):
            return "VM crashed with error \(status): \(output)"
        case .wrongArch:
            return "Wrong CPU type"
        case .killswitchExpired:
            return "Build expired"
        case .startFailed(let cause):
            return "Failed to start VM: \(cause?.localizedDescription ?? "daemon stopped unexpectedly")"
        case .startTimeout(let cause):
            return "Timed out waiting for VM to start: \(cause?.localizedDescription ?? "timeout")"
        case .stopError(let cause):
            return "Failed to stop VM: \(fmtRpc(cause))"
        case .setupError(let cause):
            return "Failed to do setup: \(fmtRpc(cause))"
        case .dockerError(let cause):
            return "Failed to check Docker: \(fmtRpc(cause))"
        case .configRefresh(let cause):
            return "Failed to get settings: \(fmtRpc(cause))"
        case .configPatchError(let cause):
            return "Failed to update settings: \(fmtRpc(cause))"

        case .startError(let cause):
            return "Failed to start machine manager: \(fmtRpc(cause))"
        case .listRefresh(let cause):
            return "Failed to get machines: \(fmtRpc(cause))"
        case .defaultError(let cause):
            return "Failed to set default machine: \(fmtRpc(cause))"
        case .containerStopError(let cause):
            return "Failed to stop machine: \(fmtRpc(cause))"
        case .containerStartError(let cause):
            return "Failed to start machine: \(fmtRpc(cause))"
        case .containerDeleteError(let cause):
            return "Failed to delete machine: \(fmtRpc(cause))"
        case .containerCreateError(let cause):
            return "Failed to create machine: \(fmtRpc(cause))"
        }
    }

    var shouldShowLogs: Bool {
        switch self {
        case .spawnError:
            return true
        // not .spawnExit. if spawn-daemon exited, it means daemon never even started so we have logs from stderr.
        case .startFailed:
            return true
        case .startTimeout:
            return true
        case .stopError:
            return true

        case .startError:
            return true
        case .listRefresh:
            return true

        default:
            return false
        }
    }

    var recoverySuggestion: String? {
        if shouldShowLogs {
            return "Check logs for more details."
        }

        switch self {
        case .wrongArch:
            return "Please download the Apple Silicon version of OrbStack."
        case .killswitchExpired:
            return "Please download the latest version of OrbStack."

        default:
            return nil
        }
    }

    var cause: Error? {
        switch self {
        case .spawnError(let cause):
            return cause
        case .spawnExit:
            return nil
        case .wrongArch:
            return nil
        case .killswitchExpired:
            return nil
        case .startFailed(let cause):
            return cause
        case .startTimeout(let cause):
            return cause
        case .stopError(let cause):
            return cause
        case .setupError(let cause):
            return cause
        case .dockerError(let cause):
            return cause
        case .configRefresh(let cause):
            return cause
        case .configPatchError(let cause):
            return cause

        case .startError(let cause):
            return cause
        case .listRefresh(let cause):
            return cause
        case .defaultError(let cause):
            return cause
        case .containerStopError(let cause):
            return cause
        case .containerStartError(let cause):
            return cause
        case .containerDeleteError(let cause):
            return cause
        case .containerCreateError(let cause):
            return cause
        }
    }

    // Don't report expected errors to Sentry
    var ignoreSentry: Bool {
        switch self {
        case .wrongArch:
            return true
        case .killswitchExpired:
            return true

        default:
            return false
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

@MainActor
class VmViewModel: ObservableObject {
    private let daemon = DaemonManager()
    private let vmgr = VmService(client: newRPCClient("http://127.0.0.1:62420"))
    private let scon = SconService(client: newRPCClient("http://127.0.0.1:62421"))

    @Published private(set) var state = VmState.stopped {
        didSet {
            if state == .running {
                reachedRunning = true
            }
        }
    }

    @Published private(set) var containers: [ContainerRecord]?
    @Published private(set) var error: VmError? {
        didSet {
            if let error = error {
                NSLog("Error: \(error)")
                if !error.ignoreSentry {
                    SentrySDK.capture(error: error)
                }
            }
        }
    }

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

    private func setStateAsync(_ state: VmState) {
        DispatchQueue.main.async {
            self.state = state
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
                setStateAsync(.starting)
            } catch let processError as ProcessError {
                DispatchQueue.main.async {
                    self.state = .stopped
                    self.error = VmError.spawnExit(status: processError.status, output: processError.output)
                }
            } catch {
                DispatchQueue.main.async {
                    self.state = .stopped
                    self.error = VmError.spawnError(cause: error)
                }
            }
        }
        setStateAsync(.spawning)
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

            try await Task.sleep(nanoseconds: startPollInterval)
            // bail out if daemon exited
            // TODO reduce timeout when gui handles rosetta install
            if await !daemon.isRunning() {
                setStateAsync(.stopped)
                throw VmError.startFailed(cause: lastError)
            }
            if DispatchTime.now() > deadline {
                setStateAsync(.stopped)
                throw VmError.startTimeout(cause: lastError)
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
            try await Task.sleep(nanoseconds: startPollInterval)
            // bail out if daemon exited
            // TODO reduce timeout when gui handles rosetta install
            if await !daemon.isRunning() {
                setStateAsync(.stopped)
                throw VmError.startFailed(cause: lastError)
            }
            if DispatchTime.now() > deadline {
                setStateAsync(.stopped)
                throw VmError.startTimeout(cause: lastError)
            }
        }
    }

    @MainActor
    func refreshList() async throws {
        guard state < .stopping else {
            return
        }

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
        // this doubles as a daemon ping to update state if started from CLI while stopped in GUI
        if state == .stopped {
            let daemonRunning = await daemon.isRunning()
            if daemonRunning {
                // trigger normal start flow
                await start()
                return
            }
        }

        do {
            try await refreshList()
        } catch {
            // check daemon process state
            let daemonRunning = await daemon.isRunning()
            if !daemonRunning {
                // daemon stopped, update state
                self.state = .stopped

                if reachedRunning {
                    // stopped by someone else, and we've successfully started. suppress the error
                    // TODO: check old state?
                    return
                }
            }

            self.error = VmError.listRefresh(cause: error)
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
            self.error = VmError.dockerError(cause: error)
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
            self.error = VmError.configRefresh(cause: error)
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
                    NSLog("setup admin error: \(error)")
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
            self.error = VmError.setupError(cause: error)
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
            self.error = VmError.spawnError(cause: error)
            return
        }

        // this includes wait
        NSLog("refresh: start")
        await tryRefreshList()
        await tryRefreshConfig()
        self.state = .running
    }

    @MainActor
    func stop() async {
        self.state = .stopping
        do {
            try await vmgr.stop()
        } catch {
            // if it's stopped, ignore the error. ("The network connection was lost." NSURLErrorNetworkConnectionLost)
            if await !daemon.isRunning() {
                self.state = .stopped
                return
            }

            self.error = VmError.stopError(cause: error)
        }
        self.state = .stopped
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
            self.error = VmError.containerStopError(cause: error)
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
            self.error = VmError.containerStartError(cause: error)
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
            self.error = VmError.containerDeleteError(cause: error)
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
            self.error = VmError.containerCreateError(cause: error)
        }
    }

    func patchConfig(_ patch: VmConfigPatch) async throws {
        try await vmgr.patchConfig(patch)
        try await refreshConfig()
    }

    @MainActor
    func tryPatchConfig(_ patch: VmConfigPatch) async {
        do {
            try await patchConfig(patch)
        } catch {
            self.error = VmError.configPatchError(cause: error)
        }
    }

    @MainActor
    func trySetDefaultContainer(_ record: ContainerRecord) async {
        do {
            try await scon.setDefaultContainer(record)
        } catch {
            self.error = VmError.defaultError(cause: error)
        }
    }

    @MainActor
    func tryRefreshSshConfigWritable() async {
        do {
            isSshConfigWritable = try await vmgr.isSshConfigWritable()
        } catch {
            NSLog("ssh config writable error: \(error)")
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
