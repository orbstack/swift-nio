//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import SwiftJSONRPC
import Sentry
import Virtualization
import Combine
import Defaults

private let startPollInterval: UInt64 = 100 * 1000 * 1000 // 100 ms
private let dockerSystemDfRatelimit = 1.0 * 60 * 60 // 1 hour
private let maxAdminDismissCount = 2 // auto-disable

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
    case configUpdateError(cause: Error)
    case resetDataError(cause: Error)
    case eventDecodeError(cause: Error)

    // vmgr - async
    case drmWarning(event: UIEvent.DrmWarning)

    // docker
    case dockerContainerActionError(action: String, cause: Error)
    case dockerVolumeActionError(action: String, cause: Error)
    case dockerImageActionError(action: String, cause: Error)
    case dockerComposeActionError(action: String, cause: Error)
    case dockerConfigSaveError(cause: Error)
    // migration
    case dockerMigrationError(status: Int, output: String)

    // k8s
    case k8sResourceActionError(kid: K8SResourceId, action: K8SResourceAction, cause: Error)

    // scon
    case startError(cause: Error)
    case defaultError(cause: Error)
    case containerStopError(cause: Error)
    case containerStartError(cause: Error)
    case containerRestartError(cause: Error)
    case containerDeleteError(cause: Error)
    case containerCreateError(cause: Error)
    case containerRenameError(cause: Error)

    // helper
    case privHelperUninstallError(cause: Error)

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
        case .configUpdateError:
            return "Can’t change settings"
        case .resetDataError:
            return "Can’t reset data"
        case .eventDecodeError:
            return "Can’t get info"

        case .drmWarning:
            return "Can’t verify license. OrbStack will stop working soon."

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

        case .k8sResourceActionError(let kid, let action, _):
            return "Can’t \(action.userDesc) \(kid.typeDesc)"

        case .startError:
            return "Failed to start machine manager"
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

        case .privHelperUninstallError:
            return "Can’t uninstall helper"
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
            switch reason {
            case .drm:
                return """
                       To fix this:
                           • Check your internet connection
                           • Make sure api-license.orbstack.dev isn't blocked
                           • Check your proxy in Settings > Network
                           • Make sure your date and time are correct
                       """
            case .dataCorruption:
                return """
                       OrbStack data is corrupted and cannot be recovered.

                       To delete the data and start fresh, run "orb reset" in Terminal.

                       In the future, avoid unclean shutdowns to prevent this from happening again.
                       """
            default:
                return output
            }
        case .drmWarning(let event):
            return """
                   \(event.lastError)

                   To fix this:
                       • Check your internet connection
                       • Make sure api-license.orbstack.dev isn't blocked
                       • Check your proxy in Settings > Network
                       • Make sure your date and time are correct
                   """
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
        case .configUpdateError(let cause):
            return cause
        case .resetDataError(let cause):
            return cause
        case .eventDecodeError(let cause):
            return cause

        case .drmWarning:
            return nil

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

        case .k8sResourceActionError(_, _, let cause):
            return cause

        case .startError(let cause):
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

        case .privHelperUninstallError(let cause):
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
            if action == "delete",
               fmtRpc(cause) == "volume in use" {
                return true
            }
        case .dockerImageActionError(let action, let cause):
            if action == "delete",
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

    let privHelper = PHClient()

    // TODO: fix state machine to deal with restarting
    @Published private(set) var isVmRestarting = false
    @Published private(set) var state = VmState.stopped {
        didSet {
            if state == .running {
                reachedRunning = true
            }

            if state == .stopped {
                // clear state
                containers = nil
                config = nil
                appliedConfig = nil

                dockerContainers = nil
                dockerVolumes = nil
                dockerImages = nil
                dockerSystemDf = nil

                k8sPods = nil
                k8sServices = nil
            }
        }
    }

    @Published private(set) var containers: [ContainerRecord]?
    @Published private(set) var error: VmError?

    @Published private(set) var appliedConfig: VmConfig? // usually from last start
    @Published private(set) var config: VmConfig?
    private(set) var reachedRunning = false

    @Published var presentProfileChanged: ProfileChangedAlert?
    @Published var presentAddPaths: AddPathsAlert?
    @Published var presentCreateMachine = false
    @Published var presentCreateVolume = false

    // Docker
    @Published private(set) var dockerContainers: [DKContainer]?
    @Published private(set) var dockerVolumes: [DKVolume]?
    @Published private(set) var dockerImages: [DKImage]?
    @Published private(set) var dockerSystemDf: DKSystemDf?

    // Kubernetes
    @Published private(set) var k8sPods: [K8SPod]?
    @Published private(set) var k8sServices: [K8SService]?

    // TODO move to WindowTracker
    var openDockerLogWindowIds: Set<DockerContainerId> = []
    var openK8sLogWindowIds: Set<K8SResourceId> = []
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

        daemon.uiEvents.sink { [weak self] rawEvent in
            guard let self else { return }

            Task { @MainActor in
                // workaround for Codable not being to decode polymorphic
                // events can also be coalesced, so check each one
                if let event = rawEvent.vmgr {
                    // go through spawn-daemon for simplicity and ignore pid
                    if let newDaemonPid = event.newDaemonPid {
                        NSLog("Daemon started: pid \(newDaemonPid)")
                        await self.tryStartDaemon()
                    }
                    if event.stateReady {
                        // just reached vmcontrol server ready after a new start
                        await self.onDaemonReady()
                    }
                    if let vmConfig = event.vmConfig {
                        self.onNewVmgrConfig(config: vmConfig)
                    }
                }

                if let event = rawEvent.scon {
                    NSLog("machines changed")
                    if let containers = event.currentMachines {
                        self.onNewSconMachines(allContainers: containers)
                    }
                }

                if let event = rawEvent.docker {
                    NSLog("docker changed")
                    if let containers = event.currentContainers {
                        self.onNewDockerContainers(containers: containers)
                    }
                    if let volumes = event.currentVolumes {
                        self.onNewDockerVolumes(rawVolumes: volumes)
                    }
                    if let images = event.currentImages {
                        self.onNewDockerImages(rawImages: images)
                    }
                    if let systemDf = event.currentSystemDf {
                        self.onNewDockerSystemDf(resp: systemDf)
                    }
                    if event.stopped {
                        self.dockerContainers = nil
                        self.dockerVolumes = nil
                        self.dockerImages = nil
                        self.dockerSystemDf = nil
                    }
                }

                if let event = rawEvent.drmWarning {
                    NSLog("drm warning")
                    self.setError(.drmWarning(event: event))
                }

                if let event = rawEvent.k8s {
                    NSLog("k8s changed")
                    if let pods = event.currentPods {
                        self.k8sPods = pods
                    }
                    if let services = event.currentServices {
                        self.k8sServices = services
                    }
                    if event.stopped {
                        self.k8sPods = nil
                        self.k8sServices = nil
                    }
                }
            }
        }.store(in: &cancellables)

        daemon.uiEventErrors.sink { [weak self] error in
            guard let self else { return }

            Task { @MainActor in
                self.setError(.eventDecodeError(cause: error))
            }
        }.store(in: &cancellables)
    }

    private func advanceStateAsync(_ state: VmState) {
        DispatchQueue.main.async {
            if state > self.state {
                self.state = state
            }
        }
    }

    private func setError(_ error: VmError) {
        if let cause = error.cause,
           case let cause as CancellationError = cause {
            NSLog("Ignoring cancellation error: \(cause)")
            return
        }

        NSLog("Error: \(error)")
        if !error.ignoreSentry {
            SentrySDK.capture(error: error)
        }

        // attempted workaround for SwiftUI main thread hangs:
        // if there's already an error, don't overwrite it
        if self.error == nil {
            self.error = error
        }
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

                advanceStateAsync(.starting)
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

    @MainActor
    private func onNewSconMachines(allContainers: [ContainerRecord]) {
        let isFirstContainers = containers == nil

        // filter into running and stopped
        let runningContainers = allContainers.filter { $0.running }
        let stoppedContainers = allContainers.filter { !$0.running }
        // sort alphabetically by name within each group
        containers = runningContainers.sorted { $0.name < $1.name } +
                stoppedContainers.sorted { $0.name < $1.name }

        // first new scon containers = scon is now running
        if isFirstContainers {
            advanceStateAsync(.running)
        }
    }

    @MainActor
    private func onNewDockerContainers(containers: [DKContainer]) {
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
    private func onNewDockerVolumes(rawVolumes: [DKVolume]) {
        // sort volumes
        let volumes = rawVolumes.sorted { $0.name < $1.name }
        dockerVolumes = volumes
    }

    @MainActor
    private func onNewDockerImages(rawImages: [DKImage]) {
        // sort images
        let images = rawImages.sorted { $0.userTag < $1.userTag }
        dockerImages = images
    }

    @MainActor
    private func onNewDockerSystemDf(resp: DKSystemDf) {
        dockerSystemDf = resp
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
    private func onNewVmgrConfig(config: VmConfig) {
        // first config after start = applied
        if appliedConfig == nil {
            appliedConfig = config
        }
        self.config = config
    }

    @MainActor
    func doSetup() async throws {
        let info = try await vmgr.startSetup()

        if let pathCmd = info.alertProfileChanged {
            presentProfileChanged = ProfileChangedAlert(profileRelPath: pathCmd)
        }

        if let paths = info.alertRequestAddPaths {
            presentAddPaths = AddPathsAlert(paths: paths)
        }

        // need to do anything?
        if let cmds = info.adminSymlinkCommands {
            // suffixed with "OrbStack is trying to install a new helper tool." but only in GUI
            if let reason = info.adminMessage {
                privHelper.installReason = reason
            }

            for cmd in cmds {
                do {
                    try await privHelper.symlink(src: cmd.src, dest: cmd.dest)
                } catch PHError.canceled {
                    // ignore: user canceled
                    if Defaults[.adminDismissCount] >= maxAdminDismissCount {
                        // try to disable admin
                        trySetConfigKey(\.setupUseAdmin, false)
                    }
                    return
                }
            }
        }
    }

    func initLaunch() {
        // do this part synchronously to avoid UI flicker on launch
        if !_trySpawnDaemon() {
            return
        }

        // async part
        Task { @MainActor in
            await tryStartDaemon(doSpawn: false)
        }
    }

    private func _trySpawnDaemon() -> Bool {
        do {
            try spawnDaemon()
        } catch VmError.wrongArch {
            setError(.wrongArch)
            return false
        } catch VmError.virtUnsupported {
            setError(.virtUnsupported)
            return false
        } catch VmError.killswitchExpired {
            setError(.killswitchExpired)
            return false
        } catch {
            setError(.spawnError(cause: error))
            return false
        }
        return true
    }

    @MainActor
    func tryStartDaemon(doSpawn: Bool = true) async {
        if doSpawn {
            if !_trySpawnDaemon() {
                return
            }
        }

        // this will fail if just started, but succeed if already running
        // same with scon.
        do {
            try await vmgr.guiReportStarted()
            try await scon.internalGuiReportStarted()
        } catch {
            // ignore
        }
    }

    @MainActor
    private func onDaemonReady() async {
        do {
            try await vmgr.guiReportStarted()
            try await scon.internalGuiReportStarted()
        } catch {
            NSLog("refresh: start: report started: \(error)")
            // don't do setup
            return
        }

        NSLog("daemon ready")
        do {
            try await doSetup()
        } catch {
            setError(.setupError(cause: error))
        }
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
        isVmRestarting = true
        defer {
            isVmRestarting = false
        }

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
        await tryStartDaemon()
    }

    func stopContainer(_ record: ContainerRecord) async throws {
        try await scon.containerStop(record)
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
    }

    func startContainer(_ record: ContainerRecord) async throws {
        try await scon.containerStart(record)
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
    }

    @MainActor
    func tryDeleteContainer(_ record: ContainerRecord) async {
        do {
            try await deleteContainer(record)
        } catch {
            setError(.containerDeleteError(cause: error))
        }
    }

    @MainActor
    func tryInternalDeleteK8s() async {
        do {
            try await scon.internalDeleteK8s()
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

    func trySetConfigKey<T: Equatable>(_ keyPath: WritableKeyPath<VmConfig, T>, _ newValue: T) {
        Task { @MainActor in
            await trySetConfigKeyAsync(keyPath, newValue)
        }
    }

    func trySetConfigKeyAsync<T: Equatable>(_ keyPath: WritableKeyPath<VmConfig, T>, _ newValue: T) async {
        if var config = config {
            config[keyPath: keyPath] = newValue
            await trySetConfig(config)
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
        await tryStartDaemon()
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

    func tryDockerContainerKill(_ id: String) async {
        await doTryDockerContainerAction("kill", {
            try await vmgr.dockerContainerKill(id)
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
        await doTryDockerContainerAction("delete", {
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

    func tryDockerComposeKill(_ cid: DockerContainerId) async {
        await doTryDockerComposeAction("kill", cid: cid, args: ["kill"])
    }

    func tryDockerComposeRestart(_ cid: DockerContainerId) async {
        await doTryDockerComposeAction("restart", cid: cid, args: ["restart"])
    }

    func tryDockerComposeRemove(_ cid: DockerContainerId) async {
        await doTryDockerComposeAction("delete", cid: cid, args: ["rm", "-f", "--stop"])
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
        await doTryDockerVolumeAction("delete", {
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
    }

    func tryDockerImageRemove(_ id: String) async {
        await doTryDockerImageAction("delete", {
            try await vmgr.dockerImageRemove(id)
        })
    }

    func dismissError() {
        error = nil
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
        // JSONSerialization can't handle empty objects
        var configJsonStr = configJson
        if configJsonStr.isEmpty {
            configJsonStr = "{}"
        }

        do {
            // parse and update the json for ipv6
            // this breaks formatting, but better than regex
            var json = try JSONSerialization.jsonObject(with: configJsonStr.data(using: .utf8)!, options: []) as! [String: Any]
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

    func tryK8sPodDelete(_ kid: K8SResourceId) async {
        do {
            try await vmgr.k8sPodDelete(namespace: kid.namespace, name: kid.name)
        } catch {
            setError(.k8sResourceActionError(kid: kid, action: .delete, cause: error))
        }
    }

    func tryK8sServiceDelete(_ kid: K8SResourceId) async {
        do {
            try await vmgr.k8sServiceDelete(namespace: kid.namespace, name: kid.name)
        } catch {
            setError(.k8sResourceActionError(kid: kid, action: .delete, cause: error))
        }
    }

    func tryStartStopK8s(enable: Bool, force: Bool = false) async {
        guard force || enable != config?.k8sEnable else {
            return
        }

        await trySetConfigKeyAsync(\.k8sEnable, enable)
        k8sServices = nil
        k8sPods = nil
        // TODO fix this and add proper dirty check. this breaks dirty state of other configs
        // needs to be set first, or k8s state wrapper doesn't update
        appliedConfig = config

        if let dockerRecord = containers?.first(where: { $0.id == ContainerIds.docker }) {
            await tryRestartContainer(dockerRecord)
        }
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
