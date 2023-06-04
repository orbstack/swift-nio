//
// Created by Danny Lin on 3/1/23.
//

import Foundation
import Virtualization
import CBridge

let vzQueue = DispatchQueue(label: "dev.kdrag0n.swext.vzf")

struct ConsoleSpec: Codable {
    var readFd: Int32
    var writeFd: Int32
}

struct VzSpec: Codable {
    var cpus: Int
    var memory: UInt64
    var kernel: String
    var cmdline: String
    var console: ConsoleSpec?
    var mtu: Int
    var macAddressPrefix: String
    var networkNat: Bool
    var networkFds: [Int32]
    var rng: Bool
    var diskRootfs: String?
    var diskData: String?
    var diskSwap: String?
    var balloon: Bool
    var vsock: Bool
    var virtiofs: Bool
    var rosetta: Bool
    var sound: Bool
}

func asyncifyResult<T>(_ fn: @escaping (@escaping (Result<T, Error>) -> Void) -> Void) async throws -> T {
    return try await withCheckedThrowingContinuation { continuation in
        vzQueue.async {
            fn { result in
                switch result {
                case .success(let value):
                    continuation.resume(returning: value)
                case .failure(let error):
                    continuation.resume(throwing: error)
                }
            }
        }
    }
}

func asyncifyError(_ fn: @escaping (@escaping (Error?) -> Void) -> Void) async throws -> Void {
    return try await withCheckedThrowingContinuation { continuation in
        vzQueue.async {
            fn { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: ())
                }
            }
        }
    }
}

private enum GovzfError: Error {
    case invalidNetIndex
}

class VmWrapper: NSObject, VZVirtualMachineDelegate {
    var goHandle: uintptr_t
    private var vz: VZVirtualMachine
    private var vsockDevice: VZVirtioSocketDevice
    private var stateObserver: NSKeyValueObservation?

    init(goHandle: uintptr_t, vz: VZVirtualMachine) {
        // must init before calling super
        self.vz = vz
        self.vsockDevice = vz.socketDevices[0] as! VZVirtioSocketDevice
        self.goHandle = goHandle

        // init the rest
        super.init()
        vz.delegate = self

        stateObserver = vz.observe(\.state, options: [.new]) { [weak self] (vz, change) in
            guard let self = self else { return }
            let state = vz.state
            self.dispatchOnStateChange(state: state)
        }
    }

    deinit {
        govzf_event_Machine_deinit(self.goHandle)
    }

    func guestDidStop(_ virtualMachine: VZVirtualMachine) {
        NSLog("[VZF] Guest stopped")
    }

    func virtualMachine(_ virtualMachine: VZVirtualMachine, didStopWithError error: Error) {
        NSLog("[VZF] Guest stopped with error: \(error)")
    }

    func virtualMachine(_ virtualMachine: VZVirtualMachine, networkDevice: VZNetworkDevice, attachmentWasDisconnectedWithError error: Error) {
        NSLog("[VZF] Network device \(networkDevice) disconnected: \(error)")
    }

    func start() async throws {
        try await asyncifyResult { [self] fn in
            NSLog("[VZF] start: begin dispatch call")
            vz.start(completionHandler: { result in
                NSLog("[VZF] start: completed \(result)")
                fn(result)
            })
        }
    }

    func stop() async throws {
        try await asyncifyError { [self] fn in
            vz.stop(completionHandler: fn)
        }
    }

    func requestStop() async throws {
        try await asyncifyError { [self] fn in
            do {
                try vz.requestStop()
            } catch {
                fn(error)
            }
        }
    }

    func pause() async throws {
        try await asyncifyResult { [self] fn in
            vz.pause(completionHandler: fn)
        }
    }

    func resume() async throws {
        try await asyncifyResult { [self] fn in
            vz.resume(completionHandler: fn)
        }
    }

    func connectVsock(_ port: UInt32) async throws -> Int32 {
        let conn = try await asyncifyResult { [self] fn in
            vsockDevice.connect(toPort: port, completionHandler: fn)
        }
        // dropping connection object closes the fd, so dup it
        let fd = dup(conn.fileDescriptor)
        return fd
    }

    private func dispatchOnStateChange(state: VZVirtualMachine.State) {
        vzQueue.async {
            govzf_event_Machine_onStateChange(self.goHandle, Int32(state.rawValue))
        }
    }
}

private func createVm(goHandle: uintptr_t, spec: VzSpec) async throws -> (VmWrapper, Bool) {
    let minCpus = VZVirtualMachineConfiguration.minimumAllowedCPUCount
    let maxCpus = VZVirtualMachineConfiguration.maximumAllowedCPUCount
    let minMemory = VZVirtualMachineConfiguration.minimumAllowedMemorySize
    let maxMemory = VZVirtualMachineConfiguration.maximumAllowedMemorySize

    let config = VZVirtualMachineConfiguration()
    let bl = VZLinuxBootLoader(kernelURL: URL(fileURLWithPath: spec.kernel))
    bl.commandLine = spec.cmdline
    config.bootLoader = bl
    config.cpuCount = max(min(spec.cpus, maxCpus), minCpus)
    config.memorySize = max(min(spec.memory, maxMemory), minMemory)

    // console
    if let consoleSpec = spec.console {
        let attachment = VZFileHandleSerialPortAttachment(
                fileHandleForReading: FileHandle(fileDescriptor: consoleSpec.readFd),
                fileHandleForWriting: FileHandle(fileDescriptor: consoleSpec.writeFd))
        let console = VZVirtioConsoleDeviceSerialPortConfiguration()
        console.attachment = attachment
        config.serialPorts = [console]
    }

    // network
    var netDevices: [VZNetworkDeviceConfiguration] = []
    if spec.networkNat {
        let attachment = VZNATNetworkDeviceAttachment()
        let device = VZVirtioNetworkDeviceConfiguration()
        device.attachment = attachment
        device.macAddress = VZMACAddress(string: spec.macAddressPrefix + ":00")!
        netDevices.append(device)
    }
    for (index, networkVnetFd) in spec.networkFds.enumerated() {
        let attachment = VZFileHandleNetworkDeviceAttachment(fileHandle: FileHandle(fileDescriptor: networkVnetFd))
        if #available(macOS 13, *) {
            attachment.maximumTransmissionUnit = spec.mtu
        }
        let device = VZVirtioNetworkDeviceConfiguration()
        device.attachment = attachment
        // starting at :01
        let lastByte = UInt8(1 + index)
        device.macAddress = VZMACAddress(string: spec.macAddressPrefix + ":" + String(format: "%02x", lastByte))!
        netDevices.append(device)
    }
    config.networkDevices = netDevices

    // RNG
    if spec.rng {
        config.entropyDevices = [VZVirtioEntropyDeviceConfiguration()]
    }

    // Disks
    var disks: [VZStorageDeviceConfiguration] = []
    // 1. rootfs
    if let diskRootfs = spec.diskRootfs {
        let attachment = try VZDiskImageStorageDeviceAttachment(url: URL(fileURLWithPath: diskRootfs),
                readOnly: true, cachingMode: .cached, synchronizationMode: .fsync)
        let device = VZVirtioBlockDeviceConfiguration(attachment: attachment)
        disks.append(device)
    }
    // 2. data
    if let diskData = spec.diskData {
        let attachment = try VZDiskImageStorageDeviceAttachment(url: URL(fileURLWithPath: diskData),
                readOnly: false, cachingMode: .cached, synchronizationMode: .fsync)
        let device = VZVirtioBlockDeviceConfiguration(attachment: attachment)
        disks.append(device)
    }
    // 3. swap
    if let diskSwap = spec.diskSwap {
        // no fsync needed for swap
        let attachment = try VZDiskImageStorageDeviceAttachment(url: URL(fileURLWithPath: diskSwap),
                readOnly: false, cachingMode: .cached, synchronizationMode: .none)
        let device = VZVirtioBlockDeviceConfiguration(attachment: attachment)
        disks.append(device)
    }
    config.storageDevices = disks

    // Balloon
    if spec.balloon {
        config.memoryBalloonDevices = [VZVirtioTraditionalMemoryBalloonDeviceConfiguration()]
    }

    // Vsock
    if spec.vsock {
        config.socketDevices = [VZVirtioSocketDeviceConfiguration()]
    }

    // Virtiofs (shared)
    var fsDevices: [VZDirectorySharingDeviceConfiguration] = []
    if spec.virtiofs {
        let fs = VZVirtioFileSystemDeviceConfiguration(tag: "mac")
        let dir = VZSharedDirectory(url: URL(fileURLWithPath: "/"), readOnly: false)
        fs.share = VZSingleDirectoryShare(directory: dir)
        fsDevices.append(fs)
    }

    // Rosetta
    var rosettaCanceled = false
    #if arch(arm64)
    if #available(macOS 13, *) {
        if spec.rosetta {
            do {
                switch VZLinuxRosettaDirectoryShare.availability {
                case .notSupported:
                    // do nothing
                    break
                case .notInstalled:
                    try await VZLinuxRosettaDirectoryShare.installRosetta()
                    fallthrough
                case .installed:
                    let dir = try VZLinuxRosettaDirectoryShare()
                    let fs = VZVirtioFileSystemDeviceConfiguration(tag: "rosetta")
                    fs.share = dir
                    fsDevices.append(fs)
                @unknown default:
                    break
                }
                // check for VZErrorDomain code 9
            } catch let error as VZError {
                switch error.code {
                case .operationCancelled:
                    // do nothing
                    rosettaCanceled = true
                    break
                default:
                    throw error
                }
            } catch {
                throw error
            }
        }
    }
    #endif
    config.directorySharingDevices = fsDevices

    // Sound
    if spec.sound {
        let device = VZVirtioSoundDeviceConfiguration()
        let stream = VZVirtioSoundDeviceOutputStreamConfiguration()
        stream.sink = VZHostAudioOutputStreamSink()
        device.streams = [stream]
        config.audioDevices = [device]
    }

    // Validate
    try config.validate()

    // Create
    let vm = VZVirtualMachine(configuration: config, queue: vzQueue)
    return (VmWrapper(goHandle: goHandle, vz: vm), rosettaCanceled)
}

@_cdecl("govzf_run_NewMachine")
func post_NewMachine(goHandle: uintptr_t, configJsonStr: UnsafePointer<CChar>) -> GResultCreate {
    let config: VzSpec = decodeJson(configJsonStr)
    let result = ResultWrapper<GResultCreate>()
    Task.detached {
        do {
            let (wrapper, rosettaCanceled) = try await createVm(goHandle: goHandle, spec: config)
            // take a long-lived ref for Go
            let ptr = Unmanaged.passRetained(wrapper).toOpaque()
            result.set(GResultCreate(ptr: ptr, err: nil, rosetta_canceled: rosettaCanceled))
        } catch {
            let prettyError = "\(error)"
            result.set(GResultCreate(ptr: nil, err: strdup(prettyError), rosetta_canceled: false))
        }
    }
    return result.wait()
}

@_cdecl("govzf_run_Machine_Start")
func post_Machine_Start(ptr: UnsafeMutableRawPointer) -> GResultErr {
    NSLog("[VZF] start: begin ffi")
    return doGenericErr(ptr) { (wrapper: VmWrapper) in
        NSLog("[VZF] start: begin task")
        try await wrapper.start()
    }
}

@_cdecl("govzf_run_Machine_Stop")
func run_Machine_Stop(ptr: UnsafeMutableRawPointer) -> GResultErr {
    doGenericErr(ptr) { (wrapper: VmWrapper) in
        try await wrapper.stop()
    }
}

@_cdecl("govzf_run_Machine_RequestStop")
func run_Machine_RequestStop(ptr: UnsafeMutableRawPointer) -> GResultErr {
    doGenericErr(ptr) { (wrapper: VmWrapper) in
        try await wrapper.requestStop()
    }
}

@_cdecl("govzf_run_Machine_Pause")
func run_Machine_Pause(ptr: UnsafeMutableRawPointer) -> GResultErr {
    doGenericErr(ptr) { (wrapper: VmWrapper) in
        try await wrapper.pause()
    }
}

@_cdecl("govzf_run_Machine_Resume")
func run_Machine_Resume(ptr: UnsafeMutableRawPointer) -> GResultErr {
    doGenericErr(ptr) { (wrapper: VmWrapper) in
        try await wrapper.resume()
    }
}

@_cdecl("govzf_run_Machine_ConnectVsock")
func run_Machine_ConnectVsock(ptr: UnsafeMutableRawPointer, port: UInt32) -> GResultIntErr {
    doGenericErrInt(ptr) { (wrapper: VmWrapper) in
        Int64(try await wrapper.connectVsock(port))
    }
}

@_cdecl("govzf_run_Machine_finalize")
func run_Machine_finalize(ptr: UnsafeMutableRawPointer) {
    // drop long-lived Go ref
    Unmanaged<VmWrapper>.fromOpaque(ptr).release()
}


// TODO move this
@_cdecl("swext_ipc_notify_started")
func swext_ipc_notify_started() {
    let nc = DistributedNotificationCenter.default()
    // deliverImmediately sends even if GUI is in background
    nc.postNotificationName(.init("dev.orbstack.vmgr.private.DaemonStarted"),
            object: nil,
            userInfo: ["pid": getpid()],
            deliverImmediately: true)
}

@_cdecl("swext_ipc_notify_docker_event")
func swext_ipc_notify_docker_event(eventJsonStr: UnsafePointer<CChar>) {
    let nc = DistributedNotificationCenter.default()
    // deliverImmediately for meneu bar app
    let eventJson = String(cString: eventJsonStr)
    nc.postNotificationName(.init("dev.orbstack.vmgr.private.DockerUIEvent"),
            object: nil,
            userInfo: ["event_json": eventJson],
            deliverImmediately: true)
}
