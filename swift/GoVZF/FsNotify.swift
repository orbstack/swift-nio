//
// Created by Danny Lin on 4/14/23.
//

import Foundation
import CoreServices

private let debugPrintEvents = false

private let fseventsQueue = DispatchQueue(label: "fsevents", qos: .background)

private let npFlagCreate: UInt64 = 1 << 0
private let npFlagModify: UInt64 = 1 << 1
private let npFlagStatAttr: UInt64 = 1 << 2
private let npFlagRemove: UInt64 = 1 << 3
private let npFlagDirChange: UInt64 = 1 << 4

private let krpcMsgNotifyproxyInject: UInt32 = 1

private let virtiofsMountpoint = "/mnt/mac"

private let linuxPathMax = 4096

enum SwextFseventsError: Error {
    case createFail, streamNil, startFail
}

private func dedupeEvents(_ paths: UnsafeMutableRawPointer, _ flags: UnsafePointer<FSEventStreamEventFlags>, _ numEvents: Int) -> [String: FSEventStreamEventFlags] {
    if debugPrintEvents {
        print("---begin---")
        print("# of events: \(numEvents)")
    }
    let paths = paths.assumingMemoryBound(to: UnsafePointer<CChar>.self)

    // dedupe and coalesce flags by path
    var pathsAndFlags = [String: FSEventStreamEventFlags]()
    for i in 0..<numEvents {
        let path = String(cString: paths[i])
        var flags = flags[i]
        var flagsInt = Int(flags)
        if debugPrintEvents {
            print("path: \(path), flags: \(flags)")
            print("  ", terminator: "")
            if flagsInt & kFSEventStreamEventFlagNone != 0 {
                print("[none] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagMustScanSubDirs != 0 {
                print("[must scan subdirs] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagUserDropped != 0 {
                print("[user dropped] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagKernelDropped != 0 {
                print("[kernel dropped] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagEventIdsWrapped != 0 {
                print("[event ids wrapped] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagHistoryDone != 0 {
                print("[history done] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagRootChanged != 0 {
                print("[root changed] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagMount != 0 {
                print("[mount] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagUnmount != 0 {
                print("[unmount] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemCreated != 0 {
                print("[created] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemRemoved != 0 {
                print("[removed] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemInodeMetaMod != 0 {
                print("[inode meta mod] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemRenamed != 0 {
                print("[renamed] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemModified != 0 {
                print("[modified] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemFinderInfoMod != 0 {
                print("[finder info mod] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemChangeOwner != 0 {
                print("[change owner] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemXattrMod != 0 {
                print("[xattr mod] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemIsFile != 0 {
                print("[is file] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemIsDir != 0 {
                print("[is dir] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemIsSymlink != 0 {
                print("[is symlink] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagOwnEvent != 0 {
                print("[own event] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemIsHardlink != 0 {
                print("[is hardlink] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemIsLastHardlink != 0 {
                print("[is last hardlink] ", terminator: "")
            }
            if flagsInt & kFSEventStreamEventFlagItemCloned != 0 {
                print("[cloned] ", terminator: "")
            }
            print("")
        }

        // ignore "history done" sentinel
        if flagsInt & kFSEventStreamEventFlagHistoryDone != 0 {
            continue
        }

        // fix misreported events: if (created|modified), remove (created) if (inode meta mod) is set
        // sometimes a relatively new file that's modified will have (created | modified) set
        // differentiate: real modification always has (inode meta mod)
        // the weird events have all set: [created] [inode meta mod] [modified] [is file]
        if flagsInt & kFSEventStreamEventFlagItemCreated != 0 &&
           flagsInt & kFSEventStreamEventFlagItemModified != 0 &&
           flagsInt & kFSEventStreamEventFlagItemInodeMetaMod != 0 {
            flags = FSEventStreamEventFlags(flagsInt & ~kFSEventStreamEventFlagItemCreated)
            flagsInt = Int(flags)
        }

        if path.utf8.count > linuxPathMax {
            continue
        }

        // apply flag prefix here
        let newPath = virtiofsMountpoint + path

        if let existingFlags = pathsAndFlags[newPath] {
            pathsAndFlags[newPath] = existingFlags | flags
        } else {
            pathsAndFlags[newPath] = flags
        }
    }

    if debugPrintEvents {
        print("---end---")
    }
    return pathsAndFlags
}

// krpc
private func eventsToKrpc(_ pathsAndFlags: [String: FSEventStreamEventFlags], isDirChange: Bool) -> (UnsafeMutablePointer<UInt8>, Int) {
    var totalPathLen: Int = 0
    for (path, _) in pathsAndFlags {
        // with null terminator
        totalPathLen += path.utf8.count + 1
    }

    // prepare buffer
    let eventCount = pathsAndFlags.count
    let totalLen = 8 + 8 + eventCount*8 + totalPathLen
    let buf = UnsafeMutablePointer<UInt8>.allocate(capacity: totalLen)

    // write header
    var header = krpc_header(len: UInt32(totalLen - 8), typ: krpcMsgNotifyproxyInject)
    memcpy(buf, &header, 8)

    // write count
    var injectHdr = krpc_notifyproxy_inject(count: UInt64(eventCount))
    memcpy(buf.advanced(by: 8), &injectHdr, 8)

    // write all descs (flags)
    var offset = 8 + 8
    for (path, flags) in pathsAndFlags {
        // convert flags
        var npFlags: UInt64 = 0
        let fseFlags = Int(flags)

        if fseFlags & kFSEventStreamEventFlagItemCreated != 0 {
            npFlags |= npFlagCreate
        }
        if fseFlags & kFSEventStreamEventFlagItemModified != 0 {
            npFlags |= npFlagModify
        }
        // ignore FinderInfo, linux doesn't care about that
        // ignore ChangeOwner, virtiofs doesn't use real owner
        // ignore XattrMod, linux usually doesn't care about that
        if fseFlags & kFSEventStreamEventFlagItemInodeMetaMod != 0 {
            npFlags |= npFlagStatAttr
        }

        // dir events normally have flags=0
        if isDirChange {
            npFlags |= npFlagDirChange
        }

        var desc = UInt64(npFlags) | (UInt64(path.utf8.count) << 32)
        memcpy(buf.advanced(by: offset), &desc, 8)
        offset += 8
    }

    // write all paths
    for (path, _) in pathsAndFlags {
        // we'll add the null terminator ourselves, no need for swift c string
        memcpy(buf.advanced(by: offset), path, path.utf8.count)
        offset += path.utf8.count
        buf.advanced(by: offset).pointee = 0
        offset += 1
    }

    // finish
    return (buf, totalLen)
}

// now, the standard files monitor
private class VmNotifier {
    private var stream: FSEventStreamRef?

    init() throws {
        // FSEventStreamCreate fails with no paths
        try newStream(paths: ["/.__non_existent_path__/.xyz"])
    }

    private func newStream(paths: [String], lastEventId: UInt64 = UInt64(kFSEventStreamEventIdSinceNow)) throws {
        func callback(stream: ConstFSEventStreamRef, info: UnsafeMutableRawPointer?, numEvents: Int, paths: UnsafeMutableRawPointer, flags: UnsafePointer<FSEventStreamEventFlags>, ids: UnsafePointer<FSEventStreamEventId>) {
            let pathsAndFlags = dedupeEvents(paths, flags, numEvents)

            // send to krpc
            let (buf, len) = eventsToKrpc(pathsAndFlags, isDirChange: false)
            swext_fsevents_cb_krpc_events(buf, len)
            buf.deallocate()
        }

        let latency = 0.1
        let stream = FSEventStreamCreate(nil, callback, nil, paths as CFArray,
                lastEventId, latency,
                UInt32(kFSEventStreamCreateFlagIgnoreSelf | kFSEventStreamCreateFlagNoDefer |
                        kFSEventStreamCreateFlagFileEvents))
        guard let stream else {
            throw SwextFseventsError.createFail
        }

        FSEventStreamSetDispatchQueue(stream, fseventsQueue)
        self.stream = stream
    }

    func updatePaths(newPaths: [String]) throws {
        guard let oldStream = stream else {
            throw SwextFseventsError.streamNil
        }

        stop()

        // create a new stream
        let lastEventId = FSEventStreamGetLatestEventId(oldStream)
        try newStream(paths: newPaths, lastEventId: lastEventId)

        // start the new stream
        guard let stream else {
            throw SwextFseventsError.streamNil
        }

        guard FSEventStreamStart(stream) else {
            throw SwextFseventsError.startFail
        }
    }

    func start() throws {
        guard let stream else {
            throw SwextFseventsError.streamNil
        }

        guard FSEventStreamStart(stream) else {
            throw SwextFseventsError.startFail
        }
    }

    func stop() {
        guard let stream else {
            return
        }

        FSEventStreamStop(stream)
        FSEventStreamInvalidate(stream)
        self.stream = nil
    }
}

@_cdecl("swext_fsevents_VmNotifier_new")
func swext_fsevents_VmNotifier_new() -> UnsafeMutableRawPointer? {
    do {
        let notifier = try VmNotifier()
        // take a long-lived ref for Go
        let ptr = Unmanaged.passRetained(notifier).toOpaque()
        return ptr
    } catch {
        return nil
    }
}

@_cdecl("swext_fsevents_VmNotifier_updatePaths")
func swext_fsevents_VmNotifier_updatePaths(_ ptr: UnsafeMutableRawPointer?, _ paths: UnsafeMutablePointer<UnsafeMutablePointer<CChar>?>?, _ numPaths: Int) -> UnsafeMutablePointer<CChar>? {
    guard let ptr else {
        return strdup("ptr is nil")
    }

    guard let paths else {
        return strdup("paths is nil")
    }

    let notifier = Unmanaged<VmNotifier>.fromOpaque(ptr).takeUnretainedValue()

    var newPaths: [String] = []
    for i in 0..<numPaths {
        guard let path = paths[i] else {
            return strdup("path is nil")
        }

        newPaths.append(String(cString: path))
    }

    do {
        try notifier.updatePaths(newPaths: newPaths)
        return strdup("")
    } catch {
        return strdup("updatePaths failed: \(error)")
    }
}

@_cdecl("swext_fsevents_VmNotifier_start")
func swext_fsevents_VmNotifier_start(_ ptr: UnsafeMutableRawPointer?) -> UnsafeMutablePointer<CChar>? {
    guard let ptr else {
        return strdup("ptr is nil")
    }

    let notifier = Unmanaged<VmNotifier>.fromOpaque(ptr).takeUnretainedValue()

    do {
        try notifier.start()
        return strdup("")
    } catch {
        return strdup("start failed: \(error)")
    }
}

@_cdecl("swext_fsevents_VmNotifier_stop")
func swext_fsevents_VmNotifier_stop(_ ptr: UnsafeMutableRawPointer?) {
    guard let ptr else {
        return
    }

    let notifier = Unmanaged<VmNotifier>.fromOpaque(ptr).takeUnretainedValue()
    notifier.stop()
}

@_cdecl("swext_fsevents_VmNotifier_finalize")
func swext_fsevents_VmNotifier_finalize(_ ptr: UnsafeMutableRawPointer?) {
    guard let ptr else {
        return
    }

    let notifier = Unmanaged<VmNotifier>.fromOpaque(ptr).takeUnretainedValue()
    notifier.stop()
    Unmanaged<VmNotifier>.fromOpaque(ptr).release()
}