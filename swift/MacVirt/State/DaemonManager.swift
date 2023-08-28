//
// Created by Danny Lin on 3/5/23.
//

import Foundation
import Combine

private enum StopExitCode: Int {
    case logFatal = 1
    case goPanic = 2

    case kernelPanic = 101
    case drm = 102
    case healthCheck = 103
    case dataCorruption = 104
}

enum ExitReason: Equatable, CustomStringConvertible {
    case status(Int)
    case signal(Int)

    // from exit code
    case logFatal // 1
    case goPanic // 2
    // stop codes
    case kernelPanic
    case drm
    case healthCheck
    case dataCorruption

    // from signal
    case killed

    case unknown

    var description: String {
        switch self {
        case .status(let status):
            return "status \(status)"
        case .signal(let signal):
            return "signal \(signal)"

        case .logFatal:
            return "failed to start"
        case .goPanic:
            return "fatal error"

        case .kernelPanic:
            return "kernel panic"
        case .drm:
            return "license check failed"
        case .healthCheck:
            return "VM not responding"
        case .dataCorruption:
            return "data is corrupted"

        case .killed:
            return "killed (SIGKILL)"

        case .unknown:
            return "unknown status"
        }
    }
}

private actor PidsHolder {
    var pids: Set<Int> = []

    func add(_ pid: Int) -> Bool {
        guard !pids.contains(pid) else {
            return false
        }
        pids.insert(pid)
        return true
    }

    func remove(_ pid: Int) -> Bool {
        guard pids.contains(pid) else {
            return false
        }
        pids.remove(pid)
        return true
    }
}

enum DKUIEntity: String, Codable {
    case container = "container"
    case volume = "volume"
    case image = "image"
}

struct UIEvent: Codable {
    let started: Started?
    let docker: Docker?
    let drmWarning: DrmWarning?
    let k8s: K8s?

    struct Started: Codable {
        let pid: Int
    }

    struct Docker: Codable {
        let changed: [DKUIEntity]
    }

    struct DrmWarning: Codable {
        let lastError: String
    }

    struct K8s: Codable {
        let currentPods: [K8SPod]?
        let currentServices: [K8SService]?
    }
}

class DaemonManager {
    let uiEvents = PassthroughSubject<UIEvent, Never>()

    private let pidsHolder = PidsHolder()
    private var lastPid: Int?

    private func getPid() -> Int? {
        // read flock
        let path = FileManager.default.temporaryDirectory.path + "/orbstack-vmgr.lock"
        // don't use FileHandle - we can't catch the exception if doesn't exist
        let fd = open(path, O_RDONLY | O_CLOEXEC)
        // doesn't exist
        guard fd != -1 else {
            return nil
        }
        defer {
            close(fd)
        }

        var lock = flock()
        lock.l_type = Int16(F_WRLCK)
        lock.l_whence = Int16(SEEK_SET)
        lock.l_start = 0
        lock.l_len = 0

        let ret = fcntl(fd, F_GETLK, &lock)
        guard ret != -1 else {
            NSLog("Error getting lock information: \(errno)")
            return nil
        }

        guard lock.l_type != F_UNLCK else {
            return nil
        }

        // safeguard: never return pid -1 in case of wrong lock type
        guard lock.l_pid != -1 && lock.l_pid != 0 else {
            return nil
        }

        return Int(lock.l_pid)
    }

    func checkRunningNow() -> Bool {
        // no point in using kill(pid, 0) test. flock is already atomic
        return getPid() != nil
    }

    // there are 2 ways we can get a new daemon:
    // 1. spawn-daemon returned a pid
    // 2. notification center -> flock
    //
    // we do NOT check flock to get a new pid on start, because then it'll stop during spawn-daemon upgrade
    // spawn-daemon will return an existing pid so it works out
    func monitorPid(_ pid: Int, callback: @escaping (ExitReason) -> Void) async {
        // make sure we're not already monitoring this pid
        guard await pidsHolder.add(pid) else {
            return
        }
        lastPid = pid

        Thread.detachNewThread { [self] in
            NSLog("Watching pid \(pid)")
            let kqFd = kqueue()
            guard kqFd != -1 else {
                NSLog("Error creating kqueue: \(errno)")
                return
            }
            let _ = fcntl(kqFd, F_SETFD, FD_CLOEXEC)
            defer {
                close(kqFd)
            }

            // register event
            var kev = kevent(
                ident: UInt(pid),
                filter: Int16(EVFILT_PROC),
                flags: UInt16(EV_ADD | EV_ENABLE | EV_RECEIPT),
                fflags: NOTE_EXIT | UInt32(NOTE_EXITSTATUS),
                data: 0,
                udata: nil
            )
            var ret = kevent(kqFd, &kev, 1, nil, 0, nil)
            guard ret != -1 else {
                // if errno = ESRCH, the process has already exited
                if errno == ESRCH {
                    callback(.unknown)
                } else {
                    NSLog("Error registering kevent: \(errno)")
                }
                return
            }

            // wait for exit event
            var kev2 = kevent()
            ret = kevent(kqFd, nil, 0, &kev2, 1, nil)
            guard ret != -1 else {
                NSLog("Error waiting for kevent: \(errno)")
                return
            }

            // if the process exited, we should get a NOTE_EXIT
            guard kev2.fflags & NOTE_EXIT != 0 else {
                NSLog("Unexpected kevent: \(kev2.fflags)")
                return
            }

            let waitStatus = kev2.data
            // extract status or signal
            var reason: ExitReason
            if waitStatus & 0x7f != 0 {
                let signal = waitStatus & 0x7f
                switch signal {
                case 9: // SIGKILL
                    reason = .killed
                default:
                    reason = .signal(signal)
                }
            } else {
                let status = waitStatus >> 8
                switch StopExitCode(rawValue: status) {
                case .logFatal:
                    reason = .logFatal
                case .goPanic:
                    reason = .goPanic
                case .kernelPanic:
                    reason = .kernelPanic
                case .drm:
                    reason = .drm
                case .healthCheck:
                    reason = .healthCheck
                case .dataCorruption:
                    reason = .dataCorruption
                default:
                    reason = .status(status)
                }
            }
            //TODO may not be right
            if pid == lastPid {
                callback(reason)
            } else {
                NSLog("Ignoring pid \(pid) exit (\(reason)): old pid")
            }
            NSLog("Daemon exited: \(reason)")

            // remove pid
            Task {
                await pidsHolder.remove(pid)
            }
        }
    }

    // subscribe to notification center and dispatch any pids with the given callback
    func monitorNotifications() {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601

        let nc = DistributedNotificationCenter.default()
        nc.addObserver(forName: .init("dev.orbstack.vmgr.private.UIEvent"), object: nil, queue: nil) { notification in
            guard let eventJson = notification.userInfo?["event"] as? String else {
                NSLog("Notification is missing data: \(notification)")
                return
            }

            do {
                let event = try decoder.decode(UIEvent.self, from: eventJson.data(using: .utf8)!)
                NSLog("Received UI event: \(event)")
                self.uiEvents.send(event)
            } catch {
                NSLog("Failed to decode notification \(notification) - error = \(error)")
            }
        }
    }
}