//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import SwiftJSONRPC
import Sentry
import Virtualization
import Combine

private let startPollInterval: UInt64 = 100 * 1000 * 1000 // 100 ms
private let dockerSystemDfRatelimit = 1.0 * 60 * 60 // 1 hour

enum VmState: Int, Comparable {
    case stopped
    case spawning
    case starting
    case running
    case stopping

    static func <(lhs: VmState, rhs: VmState) -> Bool {
        lhs.rawValue < rhs.rawValue
    }

    var userState: String {
        switch self {
        case .stopped:
            return "Not Running"
        case .spawning:
            return "Starting"
        case .starting:
            return "Starting"
        case .running:
            return "Running"
        case .stopping:
            return "Stopping"
        }
    }
}

private let startTimeout = 3 * 60 * 1000 * 1000 * 1000 // 3 minutes

enum VmError: LocalizedError, CustomNSError, Equatable {
    // VM
    case spawnError(cause: Error)
    case spawnExit(status: Int32, output: String)
    case vmgrExit(reason: ExitReason, logOutput: String)
    case wrongArch
    case virtUnsupported
    case killswitchExpired
    case startTimeout(cause: Error?)
    case stopError(cause: Error)
    case setupError(cause: Error)
    case configRefresh(cause: Error)
    case configUpdateError(cause: Error)
    case resetDataError(cause: Error)

    // docker
    case dockerListError(cause: Error)
    case dockerContainerActionError(action: String, cause: Error)
    case dockerVolumeActionError(action: String, cause: Error)
    case dockerImageActionError(action: String, cause: Error)
    case dockerComposeActionError(action: String, cause: Error)
    case dockerConfigSaveError(cause: Error)

    // migration
    case dockerMigrationError(status: Int, output: String)

    // scon
    case startError(cause: Error)
    case listRefresh(cause: Error)
    case defaultError(cause: Error)
    case containerStopError(cause: Error)
    case containerStartError(cause: Error)
    case containerRestartError(cause: Error)
    case containerDeleteError(cause: Error)
    case containerCreateError(cause: Error)
    case containerRenameError(cause: Error)

    var errorUserInfo: [String : Any] {
        // debug desc gives most info for sentry
        [NSDebugDescriptionErrorKey: "\(self)"]
    }

    var errorDescription: String? {
        switch self {
        case .spawnError:
            return "Can’t start helper"
        case .spawnExit(let status, _):
            return "Start failed with error \(status)"
        case .vmgrExit(let reason, _):
            return "Stopped unexpectedly: \(reason)"
        case .wrongArch:
            return "Wrong CPU type"
        case .virtUnsupported:
            return "Virtualization not supported"
        case .killswitchExpired:
            return "Update required"
        case .startTimeout:
            return "Timed out waiting for start"
        case .stopError:
            return "Can’t stop"
        case .setupError:
            return "Failed to do setup"
        case .configRefresh:
            return "Can’t get settings"
        case .configUpdateError:
            return "Can’t change settings"
        case .resetDataError:
            return "Can’t reset data"

        case .dockerListError:
            return "Failed to refresh Docker"
        case .dockerContainerActionError(let action, _):
            return "Can’t \(action) container"
        case .dockerVolumeActionError(let action, _):
            return "Can’t \(action) volume"
        case .dockerImageActionError(let action, _):
            return "Can’t \(action) image"
        case .dockerComposeActionError(let action, _):
            return "Can’t \(action) project"
        case .dockerConfigSaveError:
            return "Can’t apply Docker config"

        case .dockerMigrationError:
            return "Can’t migrate Docker data"

        case .startError:
            return "Failed to start machine manager"
        case .listRefresh:
            return "Failed to load machines"
        case .defaultError:
            return "Can’t set default machine"
        case .containerStopError:
            return "Can’t stop machine"
        case .containerStartError:
            return "Can’t start machine"
        case .containerRestartError:
            return "Can’t restart machine"
        case .containerDeleteError:
            return "Can’t delete machine"
        case .containerCreateError:
            return "Can’t create machine"
        case .containerRenameError:
            return "Can’t rename machine"
        }
    }

    var shouldShowLogs: Bool {
        switch self {
        case .spawnError:
            return true
        // not .spawnExit. if spawn-daemon exited, it means daemon never even started so we have logs from stderr.
        case .vmgrExit:
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

    private var errorDesc: String? {
        switch self {
        case .spawnError(let cause):
            return cause.localizedDescription
        case .spawnExit(_, let output):
            return output
        case .vmgrExit(let reason, let output):
            if reason == .drm {
                return """
                       To fix this:
                           • Check your internet connection
                           • Make sure api-license.orbstack.dev isn't blocked
                           • Check your proxy in Settings > Network
                           • Make sure your date and time are correct
                       """
            } else {
                return output
            }

        default:
            if let cause {
                return fmtRpc(cause)
            } else {
                return nil
            }
        }
    }

    private var fixTip: String? {
        if shouldShowLogs {
            return "Check logs for more details."
        }

        switch self {
        case .wrongArch:
            return "Please download the Apple Silicon version of OrbStack."
        case .virtUnsupported:
            return "OrbStack cannot run because your computer does not support virtualization."
        case .killswitchExpired:
            return "This beta version of OrbStack is too old. Please update to continue."

        default:
            return nil
        }
    }

    var recoverySuggestion: String? {
        if let errorDesc {
            if let fixTip {
                return "\(errorDesc)\n\n\(fixTip)"
            } else {
                return errorDesc
            }
        } else {
            return fixTip
        }
    }

    var cause: Error? {
        switch self {
        case .spawnError(let cause):
            return cause
        case .spawnExit:
            return nil
        case .vmgrExit:
            return nil
        case .wrongArch:
            return nil
        case .virtUnsupported:
            return nil
        case .killswitchExpired:
            return nil
        case .startTimeout(let cause):
            return cause
        case .stopError(let cause):
            return cause
        case .setupError(let cause):
            return cause
        case .configRefresh(let cause):
            return cause
        case .configUpdateError(let cause):
            return cause
        case .resetDataError(let cause):
            return cause

        case .dockerListError(let cause):
            return cause
        case .dockerContainerActionError(_, let cause):
            return cause
        case .dockerVolumeActionError(_, let cause):
            return cause
        case .dockerImageActionError(_, let cause):
            return cause
        case .dockerComposeActionError(_, let cause):
            return cause
        case .dockerConfigSaveError(let cause):
            return cause

        case .dockerMigrationError:
            return nil

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
        case .containerRestartError(let cause):
            return cause
        case .containerDeleteError(let cause):
            return cause
        case .containerCreateError(let cause):
            return cause
        case .containerRenameError(let cause):
            return cause
        }
    }

    // Don't report expected errors to Sentry
    var ignoreSentry: Bool {
        switch self {
        case .wrongArch:
            return true
        case .virtUnsupported:
            return true
        case .killswitchExpired:
            return true
        case .dockerVolumeActionError(let action, let cause):
            if action == "remove",
               fmtRpc(cause) == "volume in use" {
                return true
            }
        case .dockerImageActionError(let action, let cause):
            if action == "remove",
               fmtRpc(cause) == "image in use" {
                return true
            }

        default:
            return false
        }

        return false
    }

    static func ==(lhs: VmError, rhs: VmError) -> Bool {
        lhs.errorDescription == rhs.errorDescription
    }
}

private enum DockerComposeError: Error {
    case composeCidExpected
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
    case is ProcessError:
        let processError = error as! ProcessError
        return "Exited with status \(processError.status):\n\(processError.output)"
    default:
        // prefer info, not localized "operation could not be completed"
        return "\(error)"
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
    private let vmgr = VmService(client: newRPCClient("http://127.0.0.1:42506"))
    private let scon = SconService(client: newRPCClient("http://127.0.0.1:42507"))

    @Published private(set) var state = VmState.stopped {
        didSet {
            if state == .running {
                reachedRunning = true
            }

            if state == .stopped {
                // clear state
                containers = nil
                config = nil
                dockerContainers = nil
                dockerVolumes = nil
                dockerImages = nil
                dockerSystemDf = nil
                lastDockerSystemDfAt = nil
            }
        }
    }

    @Published private(set) var containers: [ContainerRecord]?
    @Published private(set) var error: VmError? {
        didSet {
            if let error {
                NSLog("Error: \(error)")
                if !error.ignoreSentry {
                    SentrySDK.capture(error: error)
                }
            }
        }
    }

    @Published var creatingCount = 0
    @Published var configAtLastStart: VmConfig?
    @Published private(set) var config: VmConfig?
    private(set) var reachedRunning = false

    @Published var presentProfileChanged: ProfileChangedAlert?
    @Published var presentAddPaths: AddPathsAlert?
    @Published var presentCreateMachine = false
    @Published var presentCreateVolume = false

    // Docker
    @Published var dockerContainers: [DKContainer]?
    @Published var dockerVolumes: [DKVolume]?
    @Published var dockerImages: [DKImage]?
    @Published var dockerSystemDf: DKSystemDf?
    @Published var lastDockerSystemDfAt: Date?

    // TODO move to WindowTracker
    var openLogWindowIds: Set<DockerContainerId> = []
    var openMainWindowCount = 0
    private var cancellables = Set<AnyCancellable>()

    // Setup
    @Published private(set) var isSshConfigWritable = true

    @Published private(set) var dockerEnableIPv6 = false
    @Published var dockerConfigJson = "{\n}"

    var netBridgeAvailable: Bool {
        config?.networkBridge != false
    }

    init() {
        daemon.monitorNotifications()

        daemon.daemonNotifications.sink { _ in
            // go through spawn-daemon for simplicity and ignore pid
            Task { @MainActor in
                await self.tryStartAndWait()
            }
        }.store(in: &cancellables)

        daemon.dockerNotifications.sink { event in
            Task { @MainActor in
                let doContainers = event.changed.contains(.container)
                let doVolumes = event.changed.contains(.volume)
                await self.tryRefreshDockerList(doContainers: doContainers, doVolumes: doVolumes)
            }
        }.store(in: &cancellables)
    }

    private func setStateAsync(_ state: VmState) {
        DispatchQueue.main.async {
            self.state = state
        }
    }

    private func setError(_ error: VmError) {
        if let cause = error.cause,
           case let cause as CancellationError = cause {
            NSLog("Ignoring cancellation error: \(cause)")
            return
        }

        self.error = error
    }

    private func spawnDaemon() throws {
        guard state == .stopped else {
            return
        }

        guard !processIsTranslated() else {
            throw VmError.wrongArch
        }

        guard VZVirtualMachine.isSupported else {
            throw VmError.virtUnsupported
        }

        guard !killswitchExpired() else {
            throw VmError.killswitchExpired
        }

        // on MainActor
        state = .spawning
        Task {
            do {
                let newPidStr = try await runProcessChecked(AppConfig.vmgrExe, ["spawn-daemon"])
                let newPid = Int(newPidStr.trimmingCharacters(in: .whitespacesAndNewlines))
                guard let newPid else {
                    throw VmError.spawnError(cause: ProcessError(status: 0, output: "Invalid pid: \(newPidStr)"))
                }

                setStateAsync(.starting)
                await daemon.monitorPid(newPid) { reason in
                    self.onPidExit(reason)
                }
            } catch let processError as ProcessError {
                DispatchQueue.main.async {
                    self.state = .stopped
                    self.setError(.spawnExit(status: processError.status, output: processError.output))
                }
            } catch {
                DispatchQueue.main.async {
                    self.state = .stopped
                    self.setError(.spawnError(cause: error))
                }
            }
        }
    }

    private func onPidExit(_ reason: ExitReason) {
        switch reason {
        case .status(let status):
            DispatchQueue.main.async {
                self.state = .stopped
                if status != 0 {
                    self.setError(self.makeVmgrExitError(reason))
                }
            }
        default:
            DispatchQueue.main.async {
                self.state = .stopped
                self.setError(self.makeVmgrExitError(reason))
            }
        }
    }

    private func waitForVM() async throws {
        // wait for at least .starting
        await waitForStateAtLeast(.starting)

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
            // bail out if daemon exited (next call will fail)
            if self.state == .stopped {
                NSLog("poll VM: daemon exited")
                return
            }
            // TODO reduce timeout when gui handles rosetta install
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
            // bail out if daemon exited (next call will fail)
            if self.state == .stopped {
                NSLog("poll scon: daemon exited")
                return
            }
            // TODO reduce timeout when gui handles rosetta install
            if DispatchTime.now() > deadline {
                setStateAsync(.stopped)
                throw VmError.startTimeout(cause: lastError)
            }
        }

        setStateAsync(.running)
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
        do {
            try await refreshList()
        } catch {
            // ignore if daemon exited
            // just in case kqueue didn't get the message yet, check now
            if self.state == .stopped || !daemon.checkRunningNow() {
                return
            }

            setError(.listRefresh(cause: error))
        }
    }

    @MainActor
    private func refreshDockerContainersList() async throws {
        let containers = try await vmgr.dockerContainerList()
        // preprocess
        dockerContainers = containers.map { container in
            var container = container

            // filter ports
            container.ports = container.ports.filter { origPort in
                // only include ones with public ports
                // private port only = EXPOSE w/o forward
                guard origPort.publicPort != nil else {
                    return false
                }

                // remove 127.0.0.1 if ::1 exists
                if origPort.ip == "127.0.0.1" {
                    return !container.ports.contains(where: { $0.ip == "::1" && $0.publicPortInt == origPort.publicPortInt })
                }
                // remove 0.0.0.0 if :: exists
                if origPort.ip == "0.0.0.0" {
                    return !container.ports.contains(where: { $0.ip == "::" && $0.publicPortInt == origPort.publicPortInt })
                }
                return true
            }

            // sort ports
            container.ports.sort { $0.publicPortInt < $1.publicPortInt }

            // sort mounts
            container.mounts.sort { "\($0.source)\($0.destination)" < "\($1.source)\($1.destination)" }

            return container
        }
    }

    @MainActor
    func refreshDockerVolumesList() async throws {
        let resp = try await vmgr.dockerVolumeList()
        // sort volumes
        let volumes = resp.volumes.sorted { $0.name < $1.name }
        dockerVolumes = volumes
    }

    @MainActor
    func refreshDockerImagesList() async throws {
        let rawImages = try await vmgr.dockerImageList()
        // sort images
        let images = rawImages.sorted { $0.userTag < $1.userTag }
        dockerImages = images
    }

    @MainActor
    func refreshDockerSystemDf() async throws {
        let resp = try await vmgr.dockerSystemDf()
        dockerSystemDf = resp
    }

    @MainActor
    func refreshDockerList(doContainers: Bool, doVolumes: Bool, doImages: Bool, doSystemDf: Bool) async throws {
        guard state < .stopping else {
            return
        }

        // it's vmgr but need to wait for scon
        try await waitForScon()

        if doContainers {
            try await refreshDockerContainersList()
        }
        if doVolumes {
            try await refreshDockerVolumesList()
        }
        if doImages {
            try await refreshDockerImagesList()
        }
        if doSystemDf {
            if shouldUpdateDockerSystemDf() {
                try await refreshDockerSystemDf()
                lastDockerSystemDfAt = Date()
            }
        }
    }

    // system df is slow - skip by default
    @MainActor
    func tryRefreshDockerList(doContainers: Bool = true, doVolumes: Bool = true, doImages: Bool = true, doSystemDf: Bool = false) async {
        do {
            try await refreshDockerList(doContainers: doContainers, doVolumes: doVolumes, doImages: doImages, doSystemDf: doSystemDf)
        } catch {
            // ignore if stopped
            if let machines = containers,
               let dockerRecord = machines.first(where: { $0.id == ContainerIds.docker }),
               !dockerRecord.running {
                return
            }

            // also ignore if vm not running
            if self.state != .running || !daemon.checkRunningNow() {
                return
            }

            setError(.dockerListError(cause: error))
        }
    }

    @MainActor
    func isDockerRunning() -> Bool {
        if let containers,
           let dockerContainer = containers.first(where: { $0.id == ContainerIds.docker }),
           dockerContainer.state == .running || dockerContainer.state == .starting {
            return true
        }

        return false
    }

    @MainActor
    func maybeRefreshDockerList(doContainers: Bool = true, doVolumes: Bool = true, doImages: Bool = true, doSystemDf: Bool = false) async throws {
        // will cause feedback loop if docker is stopped
        // because querying docker engine socket will start it
        if isDockerRunning() {
            try await refreshDockerList(doContainers: doContainers, doVolumes: doVolumes, doImages: doImages, doSystemDf: doSystemDf)
        }
    }

    @MainActor
    func maybeTryRefreshDockerList(doContainers: Bool = true, doVolumes: Bool = true, doImages: Bool = true, doSystemDf: Bool = false) async {
        // will cause feedback loop if docker is stopped
        // because querying docker engine socket will start it
        if isDockerRunning() {
            await tryRefreshDockerList(doContainers: doContainers, doVolumes: doVolumes, doImages: doImages, doSystemDf: doSystemDf)
        }
    }

    @MainActor
    func refreshConfig() async throws {
        try await waitForVM()
        config = try await vmgr.getConfig()
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
            let prompt = "\(Constants.userAppName) wants to \(reason). This is optional."
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
        await tryStartAndWait(shouldDoSetup: true)
    }

    @MainActor
    func tryStartAndWait(shouldDoSetup: Bool = false) async {
        do {
            try spawnDaemon()
        } catch VmError.wrongArch {
            setError(.wrongArch)
            return
        } catch VmError.virtUnsupported {
            setError(.virtUnsupported)
            return
        } catch VmError.killswitchExpired {
            setError(.killswitchExpired)
            return
        } catch {
            setError(.spawnError(cause: error))
            return
        }

        // this includes wait
        NSLog("refresh: start")
        // avoid feedback loop if killswitch expired
        // HACK XXX: ignore errors here - UI will trigger "real" ones
        do {
            try await refreshList()
            try await maybeRefreshDockerList()

            if shouldDoSetup {
                do {
                    try await doSetup()
                } catch {
                    setError(.setupError(cause: error))
                }
            }
        } catch {
            NSLog("refresh: start: refresh lists: \(error)")
        }
        do {
            try await refreshConfig()
            configAtLastStart = config
        } catch {
            NSLog("refresh: start: refresh config: \(error)")
        }
        NSLog("end refresh: start")
    }

    @MainActor
    func stop() async throws {
        self.state = .stopping
        do {
            try await vmgr.stop()
        } catch {
            // if it's stopped, ignore the error. ("The network connection was lost." NSURLErrorNetworkConnectionLost)
            if case let InvocationError.applicationError(cause) = error,
               let execError = cause as? HTTPRequestExecutorError,
               case let .httpClientError(clientError) = execError.reason,
               let nestedError = clientError as? NestedError<Error>,
               let urlError = nestedError.cause as? URLError,
               urlError.code == .networkConnectionLost {
                return
            } else {
                throw error
            }
        }
        // we don't set state. daemonManager callback must do it
    }

    @MainActor
    func tryStop() async {
        do {
            try await stop()
        } catch {
            setError(.stopError(cause: error))
        }
    }

    // this makes vmgr re-exec itself so we don't worry about state
    @MainActor
    func tryRestart() async {
        // stop
        do {
            try await stop()
        } catch {
            setError(.stopError(cause: error))
            return
        }

        // wait for state change from daemon manager callback
        await waitForStateEquals(.stopped)

        // start
        await tryStartAndWait()
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
            setError(.containerStopError(cause: error))
        }
    }

    func restartContainer(_ record: ContainerRecord) async throws {
        try await scon.containerRestart(record)
        try await refreshList()
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
            setError(.containerStartError(cause: error))
        }
    }

    @MainActor
    func tryRestartContainer(_ record: ContainerRecord) async {
        do {
            try await restartContainer(record)
        } catch {
            setError(.containerRestartError(cause: error))
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
            setError(.containerDeleteError(cause: error))
        }
    }

    func createContainer(name: String, distro: Distro, version: String, arch: String) async throws {
        try await scon.create(name: name, image: ImageSpec(
            distro: distro.imageKey,
            version: version,
            arch: arch,
            variant: ""
        ), userPassword: nil)
        try await refreshList()
    }

    @MainActor
    func tryCreateContainer(name: String, distro: Distro, version: String, arch: String) async {
        do {
            try await createContainer(name: name, distro: distro, version: version, arch: arch)
        } catch {
            setError(.containerCreateError(cause: error))
        }
    }

    func renameContainer(_ record: ContainerRecord, newName: String) async throws {
        try await scon.containerRename(record, newName: newName)
        try await refreshList()
    }

    @MainActor
    func tryRenameContainer(_ record: ContainerRecord, newName: String) async {
        do {
            try await renameContainer(record, newName: newName)
        } catch {
            setError(.containerRenameError(cause: error))
        }
    }

    func setConfig(_ newConfig: VmConfig) async throws {
        try await vmgr.setConfig(newConfig)
        config = newConfig
    }

    @MainActor
    func trySetConfig(_ newConfig: VmConfig) async {
        do {
            try await setConfig(newConfig)
        } catch {
            setError(.configUpdateError(cause: error))
        }
    }

    func resetData() async throws {
        do {
            try await vmgr.resetData()
        } catch {
            // if it's stopped, ignore the error. ("The network connection was lost." NSURLErrorNetworkConnectionLost)
            if case let InvocationError.applicationError(cause) = error,
               let execError = cause as? HTTPRequestExecutorError,
               case let .httpClientError(clientError) = execError.reason,
               let nestedError = clientError as? NestedError<Error>,
               let urlError = nestedError.cause as? URLError,
               urlError.code == .networkConnectionLost {
                return
            } else {
                throw error
            }
        }
    }

    // this makes vmgr re-exec itself so we don't worry about state
    @MainActor
    func tryResetData() async {
        // stop
        do {
            try await resetData()
        } catch {
            setError(.resetDataError(cause: error))
            return
        }

        // wait for state change from daemon manager callback
        await waitForStateEquals(.stopped)

        // start
        await tryStartAndWait()
    }

    @MainActor
    func trySetDefaultContainer(_ record: ContainerRecord) async {
        do {
            try await scon.setDefaultContainer(record)
        } catch {
            setError(.defaultError(cause: error))
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

    @MainActor
    private func doTryDockerContainerAction(_ label: String, _ action: () async throws -> Void) async {
        do {
            try await action()
        } catch {
            setError(.dockerContainerActionError(action: "\(label)", cause: error))
        }
    }

    func tryDockerContainerStart(_ id: String) async {
        await doTryDockerContainerAction("start", {
            try await vmgr.dockerContainerStart(id)
        })
    }

    func tryDockerContainerStop(_ id: String) async {
        await doTryDockerContainerAction("stop", {
            try await vmgr.dockerContainerStop(id)
        })
    }

    func tryDockerContainerRestart(_ id: String) async {
        await doTryDockerContainerAction("restart", {
            try await vmgr.dockerContainerRestart(id)
        })
    }

    func tryDockerContainerPause(_ id: String) async {
        await doTryDockerContainerAction("pause", {
            try await vmgr.dockerContainerPause(id)
        })
    }

    func tryDockerContainerUnpause(_ id: String) async {
        await doTryDockerContainerAction("unpause", {
            try await vmgr.dockerContainerUnpause(id)
        })
    }

    func tryDockerContainerRemove(_ id: String) async {
        await doTryDockerContainerAction("remove", {
            try await vmgr.dockerContainerRemove(id)
        })
    }

    @MainActor
    private func doTryDockerComposeAction(_ label: String, cid: DockerContainerId, args: [String], requiresConfig: Bool = false) async {
        if case let .compose(project) = cid {
            // find working dir from containers
            if let containers = dockerContainers,
               let container = containers.first(where: { container in
                   container.composeProject == project
               }) {
                // only pass configs and working dir if needed for action
                // otherwise skip for robustness
                // to avoid failing on missing working dir, deleted/moved configs, invalid syntax, etc.
                // it's just not necessary

                var configArgs = [String]()
                if requiresConfig {
                    // handle multiple compose files
                    let configFiles = container.labels[DockerLabels.composeConfigFiles] ?? "docker-compose.yml"
                    for configFile in configFiles.split(separator: ",") {
                        configArgs.append("-f")
                        configArgs.append(String(configFile))
                    }

                    // pass working dir if we have it
                    if let workingDir = container.labels[DockerLabels.composeWorkingDir],
                       FileManager.default.fileExists(atPath: workingDir) {
                        configArgs.append("--project-directory")
                        configArgs.append(workingDir)
                    }
                }

                do {
                    try await runProcessChecked(AppConfig.dockerComposeExe,
                            ["-p", project] + configArgs + args,
                            env: ["DOCKER_HOST": "unix://\(Files.dockerSocket)"])
                } catch {
                    setError(.dockerComposeActionError(action: "\(label)", cause: error))
                }
            }
        } else {
            // should never happen
            setError(.dockerComposeActionError(action: "\(label)", cause: DockerComposeError.composeCidExpected))
        }
    }

    func tryDockerComposeStart(_ cid: DockerContainerId) async {
        await doTryDockerComposeAction("start", cid: cid, args: ["start"])
    }

    func tryDockerComposeStop(_ cid: DockerContainerId) async {
        await doTryDockerComposeAction("stop", cid: cid, args: ["stop"])
    }

    func tryDockerComposeRestart(_ cid: DockerContainerId) async {
        await doTryDockerComposeAction("restart", cid: cid, args: ["restart"])
    }

    func tryDockerComposeRemove(_ cid: DockerContainerId) async {
        await doTryDockerComposeAction("remove", cid: cid, args: ["rm", "-f", "--stop"])
    }

    @MainActor
    private func doTryDockerVolumeAction(_ label: String, _ action: () async throws -> Void) async {
        do {
            try await action()
        } catch {
            setError(.dockerVolumeActionError(action: "\(label)", cause: error))
        }
    }

    func tryDockerVolumeCreate(_ name: String) async {
        await doTryDockerVolumeAction("create", {
            try await vmgr.dockerVolumeCreate(DKVolumeCreateOptions(name: name, labels: nil, driver: nil, driverOpts: nil))
        })
    }

    func tryDockerVolumeRemove(_ name: String) async {
        await doTryDockerVolumeAction("remove", {
            try await vmgr.dockerVolumeRemove(name)
        })
    }

    @MainActor
    private func doTryDockerImageAction(_ label: String, _ action: () async throws -> Void) async {
        do {
            try await action()
        } catch {
            setError(.dockerImageActionError(action: "\(label)", cause: error))
        }
        // must refresh images after action because there's no events for images
        await tryRefreshDockerList(doContainers: false, doVolumes: false, doImages: true)
    }

    func tryDockerImageRemove(_ id: String) async {
        await doTryDockerImageAction("remove", {
            try await vmgr.dockerImageRemove(id)
        })
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

    func tryLoadDockerConfig() {
        do {
            let jsonText = try String(contentsOf: URL(fileURLWithPath: Files.dockerDaemonConfig), encoding: .utf8)
            dockerConfigJson = jsonText
            // can we parse it and grab ipv6?
            let json = try JSONSerialization.jsonObject(with: jsonText.data(using: .utf8)!, options: []) as! [String: Any]
            if let ipv6 = json["ipv6"] as? Bool {
                dockerEnableIPv6 = ipv6
            }
        } catch {
            NSLog("docker config load error: \(error)")
        }
    }

    func trySetDockerConfig(configJson: String, enableIpv6: Bool) async {
        do {
            // parse and update the json for ipv6
            // this breaks formatting, but better than regex
            var json = try JSONSerialization.jsonObject(with: configJson.data(using: .utf8)!, options: []) as! [String: Any]
            // don't add "ipv6" key if not needed
            let oldIpv6 = json["ipv6"] as? Bool ?? false
            if oldIpv6 != enableIpv6 {
                json["ipv6"] = enableIpv6
            }
            let data = try JSONSerialization.data(withJSONObject: json, options: [.prettyPrinted])

            // write it back out
            try data.write(to: URL(fileURLWithPath: Files.dockerDaemonConfig), options: .atomic)

            // update state
            dockerConfigJson = String(data: data, encoding: .utf8)!
            dockerEnableIPv6 = enableIpv6
        } catch {
            setError(.dockerConfigSaveError(cause: error))
        }
    }

    func waitForStateEquals(_ target: VmState) async {
        for await value in $state.first(where: { $0 == target }).values {
            if value == target {
                break
            }
        }
    }

    private func waitForStateAtLeast(_ target: VmState) async {
        for await value in $state.first(where: { $0 >= target }).values {
            if value >= target {
                break
            }
        }
    }

    private func shouldUpdateDockerSystemDf() -> Bool {
        guard let systemDf = dockerSystemDf,
              let lastUpdatedAt = lastDockerSystemDfAt else {
            return true
        }

        // system df refresh is expensive (100 ms),
        // so only do it if we're missing info for a volume,
        if let volumes = dockerVolumes,
           !volumes.allSatisfy({ vol in
               systemDf.volumes.contains(where: { sVol in vol.name == sVol.name })
           }) {
            return true
        }

        // or have not updated for a while
        return Date().timeIntervalSince(lastUpdatedAt) > dockerSystemDfRatelimit
    }

    private func makeVmgrExitError(_ reason: ExitReason) -> VmError {
        // try to read logs
        var logOutput: String
        do {
            logOutput = try String(contentsOf: URL(fileURLWithPath: Files.vmgrLog), encoding: .utf8)
        } catch {
            logOutput = "Failed to read logs: \(error)"
        }

        return .vmgrExit(reason: reason, logOutput: logOutput)
    }

    func terminateAppNow() {
        // so applicationShouldTerminate doesn't do anything special
        AppLifecycle.forceTerminate = true
        NSApp.terminate(nil)
    }
}
