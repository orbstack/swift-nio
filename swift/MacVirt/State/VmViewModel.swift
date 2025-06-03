//
// Created by Danny Lin on 2/5/23.
//

import Combine
import Defaults
import Foundation
import SecureXPC
import Sentry
import SwiftUI
import Virtualization

private let startPollInterval: UInt64 = 100 * 1000 * 1000  // 100 ms
private let dockerSystemDfRatelimit = 1.0 * 60 * 60  // 1 hour
private let maxAdminDismissCount = 2  // auto-disable

private let maxConsecutiveStatsErrors = 10

enum MenuActionRouter {
    case newVolume
    case openVolumes
    case openImages
    case importMachine
    case importVolume
    case newMachine
}

enum ToolbarAction {
    case activityMonitorStop
}

enum VmState: Int, Comparable {
    case stopped
    case spawning
    case starting
    case running
    case stopping

    static func < (lhs: VmState, rhs: VmState) -> Bool {
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

private let startTimeout = 3 * 60 * 1000 * 1000 * 1000  // 3 minutes

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
    case reportStartError(cause: Error)

    // vmgr - async
    case drmWarning(event: UIEvent.DrmWarning)

    // docker
    case dockerContainerActionError(action: String, cause: Error)
    case dockerVolumeActionError(action: String, cause: Error)
    case dockerImageActionError(action: String, cause: Error)
    case dockerComposeActionError(action: String, cause: Error)
    case dockerConfigSaveError(cause: Error)
    case dockerExitError(status: Int, message: String?)
    case dockerDfError(cause: Error)
    case dockerVolumeImportError(cause: Error)
    case dockerVolumeExportError(cause: Error)
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
    case containerCloneError(cause: Error)
    case containerImportError(cause: Error)
    case containerExportError(cause: Error)
    case statsError(cause: Error)

    // helper
    case privHelperUninstallError(cause: Error)

    // accounts
    case signOutError(cause: Error)
    case refreshDrmError(cause: Error)

    var errorUserInfo: [String: Any] {
        // debug desc gives most info for sentry
        [NSDebugDescriptionErrorKey: "\(self)"]
    }

    var errorDescription: String? {
        switch self {
        case .spawnError:
            return "Can’t start helper"
        case let .spawnExit(status, _):
            return "Start failed with error \(status)"
        case let .vmgrExit(reason, _):
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
        case .reportStartError:
            return "Can’t connect to service"

        case .drmWarning:
            return "Can’t verify license. OrbStack will stop working soon."

        case let .dockerContainerActionError(action, _):
            return "Can’t \(action) container"
        case let .dockerVolumeActionError(action, _):
            return "Can’t \(action) volume"
        case let .dockerImageActionError(action, _):
            return "Can’t \(action) image"
        case let .dockerComposeActionError(action, _):
            return "Can’t \(action) project"
        case .dockerConfigSaveError:
            return "Can’t apply Docker config"
        case let .dockerExitError(status, _):
            return "Docker stopped with error \(status)"
        case .dockerDfError:
            return "Can’t get volume sizes"
        case .dockerMigrationError:
            return "Can’t migrate Docker data"
        case .dockerVolumeImportError:
            return "Can’t import volume"
        case .dockerVolumeExportError:
            return "Can’t export volume"

        case let .k8sResourceActionError(kid, action, _):
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
        case .containerCloneError:
            return "Can’t clone machine"
        case .containerImportError:
            return "Can’t import machine"
        case .containerExportError:
            return "Can’t export machine"

        case .privHelperUninstallError:
            return "Can’t uninstall helper"

        case .signOutError:
            return "Can’t sign out"
        case .refreshDrmError:
            return "Can’t refresh account"
        case .statsError:
            return "Can’t get stats"
        }
    }

    var shouldShowLogs: Bool {
        switch self {
        case .spawnError:
            return true
        // not .spawnExit. if spawn-daemon exited, it means daemon never even started so we have logs from stderr.
        case .vmgrExit(let reason, _):
            return !reason.hasCustomDetails
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

    var shouldShowReset: Bool {
        switch self {
        case .vmgrExit(.dataCorruption, _):
            return true
        case .vmgrExit(.dataEmpty, _):
            return true
        default:
            return false
        }
    }

    private var errorDesc: String? {
        switch self {
        case let .spawnError(cause):
            return cause.localizedDescription
        case let .spawnExit(_, output):
            return output
        case let .dockerExitError(_, message):
            return message
        case let .vmgrExit(reason, output):
            return reason.detailsMessage ?? output
        case let .drmWarning(event):
            return """
                \(event.lastError)

                To fix this:
                    • Check your internet connection
                    • Make sure api-license.orbstack.dev isn't blocked
                    • Check your proxy in Settings > Network
                    • Make sure your date and time are correct
                """
        case .setupError(XPCError.connectionInvalid):
            return """
                Privileged helper failed to start.

                To fix this:
                    • Allow OrbStack in macOS Settings > General > Login Items & Extensions > Allow in the Background. This is recommended for full functionality.
                    • Make sure that third-party software is not blocking OrbStack's privileged helper.
                    • Disable admin privileges in OrbStack Settings > System.
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
            return
                "This beta version of OrbStack is too old. Please update to continue.\n\nStable versions will not require updates."

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
        case let .spawnError(cause):
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
        case let .startTimeout(cause):
            return cause
        case let .stopError(cause):
            return cause
        case let .setupError(cause):
            return cause
        case let .configUpdateError(cause):
            return cause
        case let .resetDataError(cause):
            return cause
        case let .eventDecodeError(cause):
            return cause
        case let .reportStartError(cause):
            return cause

        case .drmWarning:
            return nil

        case let .dockerContainerActionError(_, cause):
            return cause
        case let .dockerVolumeActionError(_, cause):
            return cause
        case let .dockerImageActionError(_, cause):
            return cause
        case let .dockerComposeActionError(_, cause):
            return cause
        case let .dockerConfigSaveError(cause):
            return cause
        case .dockerExitError:
            return nil
        case let .dockerDfError(cause):
            return cause
        case let .dockerVolumeImportError(cause):
            return cause
        case let .dockerVolumeExportError(cause):
            return cause
        case .dockerMigrationError:
            return nil

        case let .k8sResourceActionError(_, _, cause):
            return cause

        case let .startError(cause):
            return cause
        case let .defaultError(cause):
            return cause
        case let .containerStopError(cause):
            return cause
        case let .containerStartError(cause):
            return cause
        case let .containerRestartError(cause):
            return cause
        case let .containerDeleteError(cause):
            return cause
        case let .containerCreateError(cause):
            return cause
        case let .containerRenameError(cause):
            return cause
        case let .containerCloneError(cause):
            return cause
        case let .containerImportError(cause):
            return cause
        case let .containerExportError(cause):
            return cause
        case let .statsError(cause):
            return cause

        case let .privHelperUninstallError(cause):
            return cause

        case let .signOutError(cause):
            return cause
        case let .refreshDrmError(cause):
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
        case let .dockerVolumeActionError(action, cause):
            if action == "delete",
                fmtRpc(cause) == "volume in use"
            {
                return true
            }
        case let .dockerImageActionError(action, cause):
            if action == "delete",
                fmtRpc(cause) == "image in use"
            {
                return true
            }

        default:
            return false
        }

        return false
    }

    static func == (lhs: VmError, rhs: VmError) -> Bool {
        lhs.errorDescription == rhs.errorDescription
    }
}

private enum DockerComposeError: Error {
    case composeCidExpected
}

private func fmtRpc(_ error: Error) -> String {
    switch error {
    case let error as ProcessError:
        return "Exited with status \(error.status):\n\(error.stderr)"
    case let error as RPCError:
        return error.errorDescription ?? "\(error)"
    default:
        // prefer info, not localized "operation could not be completed"
        return "\(error)"
    }
}

@MainActor
class VmViewModel: ObservableObject {
    private let daemon = DaemonManager()
    private let vmgr = VmService(client: JsonRPCClient(unixSocket: Files.vmgrSocket))
    private let scon = SconService(client: JsonRPCClient(unixSocket: Files.sconSocket))

    let privHelper = PHClient()

    // MARK: - New

    @Published var searchText = ""
    @Published var initialDockerContainerSelection: Set<DockerContainerId> = []
    @Published var presentAuth = false

    // the user's choice when the window is big enough
    var sidebarPrefersCollapsed = false
    var inspectorPrefersCollapsed = false

    // when pressing sidebar when super small
    var collapsedPanelOverride: Panel?
    var menuActionRouter = PassthroughSubject<MenuActionRouter, Never>()

    var toolbarActionRouter = PassthroughSubject<ToolbarAction, Never>()

    var dockerImageImportRouter = PassthroughSubject<URL, Never>()

    // TODO: proper preference-based toolbar system
    @Published var activityMonitorStopEnabled = false

    // TODO: fix state machine to deal with restarting
    @Published private(set) var isVmRestarting = false
    @Published private(set) var restartingMachines = Set<String>()

    // initial state is .spawning because we always call initLaunch on start
    @Published private(set) var state = VmState.spawning {
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

    @Published private(set) var containers: [ContainerInfo]?
    @Published var error: VmError?

    // vmgr basic state
    @Published private(set) var appliedConfig: VmConfig?  // usually from last start
    @Published private(set) var config: VmConfig?
    private(set) var reachedRunning = false

    // Docker
    @Published private(set) var dockerContainers: [DKContainer]?
    @Published private(set) var dockerVolumes: [DKVolume]?
    @Published private(set) var dockerImages: [DKSummaryAndFullImage]?
    @Published private(set) var dockerSystemDf: DKSystemDf?

    // Kubernetes
    @Published private(set) var k8sPods: [K8SPod]?
    @Published private(set) var k8sServices: [K8SService]?

    // other UI state from vmgr
    // initialize with default values for drm
    @Published var drmState: DrmState = .init(
        refreshToken: nil,
        entitlementTier: .none,
        entitlementType: .none,
        entitlementMessage: nil
    )
    {
        didSet {
            Defaults[.drmLastState] = drmState
        }
    }

    // present bindings
    // TODO: move to MainWindow, pass down by environment?
    @Published var presentProfileChanged = false
    @Published var presentAddPaths = false
    @Published var presentCreateMachine = false
    @Published var presentCreateVolume = false
    @Published var presentImportMachine: URL? = nil
    @Published var presentRequiresLicense = false
    @Published var presentImportVolume: URL? = nil

    private var cancellables = Set<AnyCancellable>()

    // Setup
    @Published private(set) var isSshConfigWritable = true

    @Published private(set) var dockerEnableIPv6 = false
    @Published var dockerConfigJson = "{\n}"

    @Published var presentForceSignIn = false

    private var consecutiveStatsErrors = 0

    private var dockerSystemDfRunning = false

    var netBridgeAvailable: Bool {
        config?.networkBridge != false
    }

    var isLicensed: Bool {
        drmState.entitlementTier != .none && !drmState.expired
    }

    init() {
        if let lastState = Defaults[.drmLastState] {
            drmState = lastState
        }

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
                    if let drmState = event.drmState {
                        self.drmState = drmState
                        self.updateForceSignIn()
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
                    if let exitEvent = event.exited {
                        self.dockerContainers = nil
                        self.dockerVolumes = nil
                        self.dockerImages = nil
                        self.dockerSystemDf = nil

                        if exitEvent.status != 0 {
                            self.setError(
                                .dockerExitError(
                                    status: exitEvent.status, message: exitEvent.message))
                        }
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

        updateForceSignIn()
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
            case let cause as CancellationError = cause
        {
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

    private func spawnDaemon(ignoreState: Bool) throws -> Task<Void, Never>? {
        guard ignoreState || state == .stopped else {
            return nil
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
        if !ignoreState {
            state = .spawning
        }

        return Task {
            do {
                // resolve symlinks in path to fix bundle ID resolution on launch
                let vmgrExePath = URL(fileURLWithPath: AppConfig.vmgrExe).resolvingSymlinksInPath()
                    .path

                let newPidStr = try await runProcessChecked(vmgrExePath, ["spawn-daemon"])
                let newPid = Int(newPidStr.trimmingCharacters(in: .whitespacesAndNewlines))
                guard let newPid else {
                    throw VmError.spawnError(
                        cause: ProcessError(status: 0, stderr: "Invalid pid: \(newPidStr)"))
                }

                advanceStateAsync(.starting)
                await daemon.monitorPid(newPid) { reason in
                    self.onPidExit(reason)
                }
            } catch let processError as ProcessError {
                DispatchQueue.main.async {
                    self.state = .stopped
                    self.setError(
                        .spawnExit(status: processError.status, output: processError.stderr))
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
        case let .status(status):
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
    private func onNewSconMachines(allContainers: [ContainerInfo]) {
        let isFirstContainers = containers == nil

        // filter into running and stopped
        let runningContainers = allContainers.filter { $0.record.running }
        let stoppedContainers = allContainers.filter { !$0.record.running }
        // sort alphabetically by name within each group
        containers =
            runningContainers.sorted { $0.record.name < $1.record.name }
            + stoppedContainers.sorted { $0.record.name < $1.record.name }

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
                    return !container.ports.contains(where: {
                        $0.ip == "::1" && $0.publicPortInt == origPort.publicPortInt
                    })
                }
                // remove 0.0.0.0 if :: exists
                if origPort.ip == "0.0.0.0" {
                    return !container.ports.contains(where: {
                        $0.ip == "::" && $0.publicPortInt == origPort.publicPortInt
                    })
                }
                return true
            }

            // sort ports
            container.ports.sort { $0.publicPortInt < $1.publicPortInt }

            // sort mounts
            container.mounts.sort {
                "\($0.source)\($0.destination)" < "\($1.source)\($1.destination)"
            }

            return container
        }
    }

    @MainActor
    private func onNewDockerVolumes(rawVolumes: [DKVolume]) {
        // sort volumes
        dockerVolumes = rawVolumes
    }

    @MainActor
    private func onNewDockerImages(rawImages: [DKSummaryAndFullImage]) {
        // sort images
        dockerImages = rawImages
    }

    @MainActor
    private func onNewDockerSystemDf(resp: DKSystemDf) {
        dockerSystemDf = resp
    }

    @MainActor
    func isDockerRunning() -> Bool {
        if let containers,
            let dockerContainer = containers.first(where: { $0.id == ContainerIds.docker }),
            dockerContainer.record.state == .running || dockerContainer.record.state == .starting
        {
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

        if info.alertProfileChanged {
            presentProfileChanged = true
        }

        if info.alertRequestAddPaths {
            presentAddPaths = true
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
        let (spawned, task) = _trySpawnDaemon(ignoreState: true)
        if !spawned {
            return
        }

        // async part once spawn-daemon finishes
        // to avoid sending gui report during 'updating'
        Task { @MainActor in
            // wait for spawn-daemon to finish before making requests
            await task?.value
            await tryStartDaemon(doSpawn: false)
        }
    }

    private func _trySpawnDaemon(ignoreState: Bool = false) -> (
        spawned: Bool, task: Task<Void, Never>?
    ) {
        do {
            return try (true, spawnDaemon(ignoreState: ignoreState))
        } catch VmError.wrongArch {
            setError(.wrongArch)
            return (false, nil)
        } catch VmError.virtUnsupported {
            setError(.virtUnsupported)
            return (false, nil)
        } catch VmError.killswitchExpired {
            setError(.killswitchExpired)
            return (false, nil)
        } catch {
            setError(.spawnError(cause: error))
            return (false, nil)
        }
    }

    @MainActor
    func tryStartDaemon(doSpawn: Bool = true) async {
        if doSpawn {
            let (spawned, task) = _trySpawnDaemon()
            if !spawned {
                return
            }

            // wait for spawn-daemon to finish before making a request
            await task?.value
        }

        // this will fail if just started, but succeed if already running
        // same with scon.
        do {
            try await vmgr.guiReportStarted()
            try await scon.internalGuiReportStarted()
        } catch RPCError.request, RPCError.eof, RPCError.app {
            // ignore

            // ignore app - "Method not found" on update
        } catch {
            setError(.reportStartError(cause: error))
            return
        }
    }

    @MainActor
    private func onDaemonReady() async {
        do {
            try await vmgr.guiReportStarted()
            try await scon.internalGuiReportStarted()
        } catch RPCError.request, RPCError.eof, RPCError.app {
            // connected to vmgr too fast. it probably crashed
            // (vmcontrol is guaranteed to be up at this point)
            // ignore and let the kqueue pid monitor handle it

            // ignore app - "Method not found" on update
        } catch {
            setError(.reportStartError(cause: error))
            return
        }

        NSLog("daemon ready")
        do {
            try await doSetup()
        } catch RPCError.request {
            // connected to vmgr too fast. it probably crashed
            // ignore and let the kqueue pid monitor handle it
        } catch {
            setError(.setupError(cause: error))
        }
    }

    @MainActor
    func stop() async throws {
        state = .stopping
        do {
            try await vmgr.stop()
        } catch RPCError.eof {
            // ignore: stopped
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
        try await scon.containerStop(record.id)
    }

    @MainActor
    func tryStopContainer(_ record: ContainerRecord) async {
        do {
            try await stopContainer(record)
        } catch {
            setError(.containerStopError(cause: error))
        }
    }

    @MainActor
    func restartContainer(_ record: ContainerRecord) async throws {
        restartingMachines.insert(record.id)
        defer {
            restartingMachines.remove(record.id)
        }

        try await scon.containerRestart(record.id)
    }

    func startContainer(_ record: ContainerRecord) async throws {
        try await scon.containerStart(record.id)
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
        try await scon.containerDelete(record.id)
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

    func createContainer(
        name: String, distro: Distro, version: String, arch: String, cloudInitUserData: URL?,
        defaultUsername: String?
    ) async throws {
        let userData = try cloudInitUserData.flatMap { try String(contentsOf: $0) }

        try await scon.create(
            CreateRequest(
                name: name,
                image: ImageSpec(
                    distro: distro.imageKey,
                    version: version,
                    arch: arch,
                    variant: ""),
                config: MachineConfig(isolated: false, defaultUsername: defaultUsername),
                userPassword: nil,
                cloudInitUserData: userData))
    }

    @MainActor
    func tryCreateContainer(
        name: String, distro: Distro, version: String, arch: String, cloudInitUserData: URL? = nil,
        defaultUsername: String? = nil
    ) async {
        do {
            try await createContainer(
                name: name, distro: distro, version: version, arch: arch,
                cloudInitUserData: cloudInitUserData, defaultUsername: defaultUsername)
        } catch {
            setError(.containerCreateError(cause: error))
        }
    }

    func importContainer(url: URL, newName: String?) async throws {
        try await scon.importContainerFromHostPath(
            ImportContainerFromHostPathRequest(newName: newName, hostPath: url.path))
    }

    @MainActor
    func tryImportContainer(url: URL, newName: String? = nil) async {
        do {
            try await importContainer(url: url, newName: newName)
        } catch {
            setError(.containerImportError(cause: error))
        }
    }

    func renameContainer(_ record: ContainerRecord, newName: String) async throws {
        try await scon.containerRename(record.id, newName: newName)
    }

    @MainActor
    func tryRenameContainer(_ record: ContainerRecord, newName: String) async {
        do {
            try await renameContainer(record, newName: newName)
        } catch {
            setError(.containerRenameError(cause: error))
        }
    }

    func cloneContainer(_ record: ContainerRecord, newName: String) async throws {
        try await scon.containerClone(record.id, newName: newName)
    }

    @MainActor
    func tryCloneContainer(_ record: ContainerRecord, newName: String) async {
        do {
            try await cloneContainer(record, newName: newName)
        } catch {
            setError(.containerCloneError(cause: error))
        }
    }

    func exportContainer(_ record: ContainerRecord, hostPath: String) async throws {
        try await scon.containerExport(record.id, hostPath: hostPath)
    }

    @MainActor
    func tryExportContainer(_ record: ContainerRecord, hostPath: String) async {
        do {
            try await exportContainer(record, hostPath: hostPath)
        } catch {
            setError(.containerExportError(cause: error))
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

    func trySetConfigKeyAsync<T: Equatable>(_ keyPath: WritableKeyPath<VmConfig, T>, _ newValue: T)
        async
    {
        if var config = config {
            config[keyPath: keyPath] = newValue
            await trySetConfig(config)
        }
    }

    func resetData() async throws {
        do {
            try await vmgr.resetData()
        } catch RPCError.eof {
            // ignore: stopped
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
            try await scon.setDefaultContainer(record.id)
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

    func dockerImportVolume(url: URL, newName: String) async throws {
        try await scon.internalDockerImportVolumeFromHostPath(
            InternalDockerImportVolumeFromHostPathRequest(newName: newName, hostPath: url.path))
    }

    @MainActor
    func tryDockerImportVolume(url: URL, newName: String) async {
        do {
            try await dockerImportVolume(url: url, newName: newName)
        } catch {
            setError(.dockerVolumeImportError(cause: error))
        }
    }

    func dockerExportVolume(volumeId: String, hostPath: String) async throws {
        try await scon.internalDockerExportVolumeToHostPath(
            InternalDockerExportVolumeToHostPathRequest(volumeId: volumeId, hostPath: hostPath))
    }

    @MainActor
    func tryDockerExportVolume(volumeId: String, hostPath: String) async {
        do {
            try await dockerExportVolume(volumeId: volumeId, hostPath: hostPath)
        } catch {
            setError(.dockerVolumeExportError(cause: error))
        }
    }

    func dockerImportImage(url: URL) async {
        await doTryDockerImageAction("import") {
            try await vmgr.dockerImageImportFromHostPath(url.path)
        }
    }

    func dockerExportImage(imageId: String, hostPath: String) async {
        await doTryDockerImageAction("export") {
            try await vmgr.dockerImageExportToHostPath(imageId, hostPath)
        }
    }

    @MainActor
    private func doTryDockerContainerAction(_ label: String, _ action: () async throws -> Void)
        async
    {
        do {
            try await action()
        } catch {
            setError(.dockerContainerActionError(action: "\(label)", cause: error))
        }
    }

    func tryDockerContainerStart(_ id: String) async {
        await doTryDockerContainerAction("start") {
            try await vmgr.dockerContainerStart(id)
        }
    }

    func tryDockerContainerStop(_ id: String) async {
        await doTryDockerContainerAction("stop") {
            try await vmgr.dockerContainerStop(id)
        }
    }

    func tryDockerContainerKill(_ id: String) async {
        await doTryDockerContainerAction("kill") {
            try await vmgr.dockerContainerKill(id)
        }
    }

    func tryDockerContainerRestart(_ id: String) async {
        await doTryDockerContainerAction("restart") {
            try await vmgr.dockerContainerRestart(id)
        }
    }

    func tryDockerContainerPause(_ id: String) async {
        await doTryDockerContainerAction("pause") {
            try await vmgr.dockerContainerPause(id)
        }
    }

    func tryDockerContainerUnpause(_ id: String) async {
        await doTryDockerContainerAction("unpause") {
            try await vmgr.dockerContainerUnpause(id)
        }
    }

    func tryDockerContainerRemove(_ id: String) async {
        await doTryDockerContainerAction("delete") {
            try await vmgr.dockerContainerRemove(id)
        }
    }

    @MainActor
    private func doTryDockerComposeAction(
        _ label: String, cid: DockerContainerId,
        args: [String], requiresConfig: Bool = false,
        ignoreError: Bool = false
    ) async {
        if case let .compose(project) = cid {
            // find working dir from containers
            if let containers = dockerContainers,
                let container = containers.first(where: { container in
                    container.composeProject == project
                })
            {
                // only pass configs and working dir if needed for action
                // otherwise skip for robustness
                // to avoid failing on missing working dir, deleted/moved configs, invalid syntax, etc.
                // it's just not necessary

                var configArgs = [String]()
                if requiresConfig {
                    // handle multiple compose files
                    let configFiles =
                        container.labels?[DockerLabels.composeConfigFiles] ?? "docker-compose.yml"
                    for configFile in configFiles.split(separator: ",") {
                        configArgs.append("-f")
                        configArgs.append(String(configFile))
                    }

                    // pass working dir if we have it
                    if let workingDir = container.labels?[DockerLabels.composeWorkingDir],
                        FileManager.default.fileExists(atPath: workingDir)
                    {
                        configArgs.append("--project-directory")
                        configArgs.append(workingDir)
                    }
                }

                do {
                    try await runProcessChecked(
                        AppConfig.dockerComposeExe,
                        ["-p", project] + configArgs + args,
                        env: ["DOCKER_HOST": "unix://\(Files.dockerSocket)"])
                } catch {
                    if !ignoreError {
                        setError(.dockerComposeActionError(action: "\(label)", cause: error))
                    }
                }
            }
        } else {
            // should never happen
            if !ignoreError {
                setError(
                    .dockerComposeActionError(
                        action: "\(label)", cause: DockerComposeError.composeCidExpected))
            }
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
        // use 'down' to remove container and networks
        // fails if config is missing, but we don't use configs anymore, so no need for 'rm' fallback
        await doTryDockerComposeAction("down", cid: cid, args: ["down", "--remove-orphans"])
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
        await doTryDockerVolumeAction("create") {
            try await vmgr.dockerVolumeCreate(
                DKVolumeCreateOptions(name: name, labels: nil, driver: nil, driverOpts: nil))
        }
    }

    func tryDockerVolumeRemove(_ name: String) async {
        await doTryDockerVolumeAction("delete") {
            try await vmgr.dockerVolumeRemove(name)
        }
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
        await doTryDockerImageAction("delete") {
            try await vmgr.dockerImageRemove(id)
        }
    }

    func tryLoadDockerConfig() {
        do {
            let jsonText = try String(
                contentsOf: URL(fileURLWithPath: Files.dockerDaemonConfig), encoding: .utf8)
            dockerConfigJson = jsonText
            // can we parse it and grab ipv6?
            let json =
                try JSONSerialization.jsonObject(with: jsonText.data(using: .utf8)!, options: [])
                as! [String: Any]
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
            var json =
                try JSONSerialization.jsonObject(
                    with: configJsonStr.data(using: .utf8)!, options: []) as! [String: Any]
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

    @MainActor
    func tryDockerSystemDf() async {
        if dockerSystemDfRunning {
            return
        }
        dockerSystemDfRunning = true
        defer {
            dockerSystemDfRunning = false
        }

        do {
            NSLog("running volume df")
            dockerSystemDf = try await scon.internalDockerFastDf()
        } catch RPCError.eof {
            // ignore: stopped
        } catch {
            setError(.dockerDfError(cause: error))
        }
    }

    func waitForStateEquals(_ target: VmState) async {
        for await value in $state.first(where: { $0 == target }).values {
            if value == target {
                break
            }
        }
    }

    func waitForNonNil<T>(_ keyPath: KeyPath<VmViewModel, Published<T?>.Publisher>) async {
        for await value in self[keyPath: keyPath].first(where: { $0 != nil }).values {
            if value != nil {
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
        // TODO: fix this and add proper dirty check. this breaks dirty state of other configs
        // needs to be set first, or k8s state wrapper doesn't update
        appliedConfig = config

        if let dockerRecord = containers?.first(where: { $0.id == ContainerIds.docker }) {
            await tryRestartContainer(dockerRecord.record)
        }
    }

    func tryGetStats(_ req: GetStatsRequest) async throws -> StatsResponse {
        do {
            return try await scon.getStats(req)
        } catch {
            consecutiveStatsErrors += 1
            if consecutiveStatsErrors >= maxConsecutiveStatsErrors {
                setError(.statsError(cause: error))
                consecutiveStatsErrors = 0
            }
            throw error
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

    func trySignOut() async {
        do {
            try await runProcessChecked(AppConfig.ctlExe, ["logout"])
        } catch {
            setError(.signOutError(cause: error))
        }
    }

    func tryRefreshDrm() async {
        do {
            try await vmgr.internalRefreshDrm()
        } catch {
            setError(.refreshDrmError(cause: error))
        }
    }

    func volumeIsMounted(_ volume: DKVolume) -> Bool {
        guard let containers = dockerContainers else {
            return false
        }

        return containers.first { container in
            container.mounts.contains { mount in
                mount.type == .volume && mount.name == volume.name
            }
        } != nil
    }

    func usedImageIds() -> Set<String> {
        guard let containers = dockerContainers else {
            return []
        }

        return Set(containers.map { $0.imageId })
    }

    // intermediate Binding that only calls `vmModel.trySetConfigKey` when the user manually drags the slider
    func bindingForConfig<T: Equatable>(
        _ keyPath: WritableKeyPath<VmConfig, T>,
        state: Binding<T>
    ) -> Binding<T> {
        Binding<T> {
            state.wrappedValue
        } set: { [self] newValue in
            state.wrappedValue = newValue
            trySetConfigKey(keyPath, newValue)
        }
    }

    func updateForceSignIn() {
        presentForceSignIn = Defaults[.mdmSsoDomain] != nil && !drmState.isSignedIn
    }
}
