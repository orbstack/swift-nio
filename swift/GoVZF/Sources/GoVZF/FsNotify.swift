//
// Created by Danny Lin on 4/14/23.
//

import CBridge
import CoreServices
import Foundation

private let debugPrintEvents = false

// background QoS throttles very hard - consider utility?
private let fseventsQueue = DispatchQueue(label: "dev.orbstack.swext.fsevents", qos: .background)

private let npFlagCreate: UInt64 = 1 << 0
private let npFlagModify: UInt64 = 1 << 1
private let npFlagStatAttr: UInt64 = 1 << 2
private let npFlagRemove: UInt64 = 1 << 3
private let npFlagDirChange: UInt64 = 1 << 4
private let npFlagRename: UInt64 = 1 << 5

private let krpcMsgNotifyproxyInject: UInt32 = 1

// up to 1 sec is ok because that's virtiofs entry_valid
// to be safe, use 500 ms
private let dirChangeDebounce: Double = 0.5
// for explicit Docker bind mount fsnotify, send events faster
// this also acts as a faster cache invalidation in such cases
private let fileWatchDebounce: Double = 0.1

private let virtiofsMountpoint = "/mnt/mac"

private let linuxPathMax = 4096

enum SwextFseventsError: Error {
    case createFail, streamNil, startFail
}

private func printDebugEvent(path: String, flags: Int) {
    if debugPrintEvents {
        print("path: \(path), flags: \(flags)")
        print("  ", terminator: "")
        if flags & kFSEventStreamEventFlagNone != 0 {
            print("[none] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagMustScanSubDirs != 0 {
            print("[must scan subdirs] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagUserDropped != 0 {
            print("[user dropped] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagKernelDropped != 0 {
            print("[kernel dropped] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagEventIdsWrapped != 0 {
            print("[event ids wrapped] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagHistoryDone != 0 {
            print("[history done] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagRootChanged != 0 {
            print("[root changed] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagMount != 0 {
            print("[mount] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagUnmount != 0 {
            print("[unmount] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemCreated != 0 {
            print("[created] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemRemoved != 0 {
            print("[removed] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemInodeMetaMod != 0 {
            print("[inode meta mod] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemRenamed != 0 {
            print("[renamed] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemModified != 0 {
            print("[modified] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemFinderInfoMod != 0 {
            print("[finder info mod] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemChangeOwner != 0 {
            print("[change owner] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemXattrMod != 0 {
            print("[xattr mod] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemIsFile != 0 {
            print("[is file] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemIsDir != 0 {
            print("[is dir] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemIsSymlink != 0 {
            print("[is symlink] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagOwnEvent != 0 {
            print("[own event] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemIsHardlink != 0 {
            print("[is hardlink] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemIsLastHardlink != 0 {
            print("[is last hardlink] ", terminator: "")
        }
        if flags & kFSEventStreamEventFlagItemCloned != 0 {
            print("[cloned] ", terminator: "")
        }
        print("")
    }
}

private func dedupeEvents(_ paths: UnsafeMutableRawPointer, _ flags: UnsafePointer<FSEventStreamEventFlags>, _ numEvents: Int) -> [String: FSEventStreamEventFlags] {
    if debugPrintEvents {
        print("---begin---")
        print("# of events: \(numEvents)")
    }
    let paths = paths.assumingMemoryBound(to: UnsafePointer<CChar>.self)

    // dedupe and coalesce flags by path
    var pathsAndFlags = [String: FSEventStreamEventFlags]()
    for i in 0 ..< numEvents {
        let path = String(cString: paths[i])
        var flags = flags[i]
        var flagsInt = Int(flags)
        if debugPrintEvents {
            printDebugEvent(path: path, flags: flagsInt)
        }

        // ignore "history done" sentinel
        if flagsInt & kFSEventStreamEventFlagHistoryDone != 0 {
            continue
        }

        // fix misreported events: if (created|modified), remove (created) if (inode meta mod) is set
        // sometimes a relatively new file that's modified will have (created | modified) set
        // differentiate: real modification always has (inode meta mod)
        // the weird events have all set: [created] [inode meta mod] [modified] [is file]
        if flagsInt & kFSEventStreamEventFlagItemCreated != 0,
           flagsInt & kFSEventStreamEventFlagItemModified != 0,
           flagsInt & kFSEventStreamEventFlagItemInodeMetaMod != 0
        {
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
    var totalPathLen = 0
    for (path, _) in pathsAndFlags {
        // with null terminator
        totalPathLen += path.utf8.count + 1
    }

    // prepare buffer
    let eventCount = pathsAndFlags.count
    let totalLen = 8 + 8 + eventCount * 8 + totalPathLen
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
        if fseFlags & kFSEventStreamEventFlagItemRenamed != 0 {
            // mainly important for atomic save
            // krpc doesn't do anything for this either; it triggers FUSE event generator
            npFlags |= npFlagRename
        }
        if fseFlags & kFSEventStreamEventFlagItemModified != 0 {
            npFlags |= npFlagModify
        }
        if fseFlags & kFSEventStreamEventFlagItemRemoved != 0 {
            // krpc doesn't do anything for this; it simply causes FUSE changed_on_revalidate to generate an event
            // because lookup will fail
            npFlags |= npFlagRemove
        }
        // ignore FinderInfo, linux doesn't care about that
        // ignore ChangeOwner, virtiofs doesn't use real owner
        // ignore XattrMod, linux usually doesn't care about that
        if fseFlags & kFSEventStreamEventFlagItemInodeMetaMod != 0 {
            // note: a minor behavior difference:
            // on Linux, open+write+close sends OPEN, MODIFY, CLOSE_WRITE
            // on macOS, open+write+close sends Modified, InodeMetaMod
            //    -> which we translate as: MODIFY, ATTRIB, CLOSE_WRITE
            // necessary, as we can't tell InodeMetaMod+Modified apart frm other attribute changes
            npFlags |= npFlagStatAttr
        }

        // if we have other events, then remove ATTRIB, unless it's a remove event
        // on Linux ATTRIB should only be sent on remove.
        // Go fsnotify lib recommends ignoring ATTRIB (which it translates to fsnotify.Chmod), and Revel framework follows it
        // but on macOS, chmod = ChangeOwner???
        // https://github.com/fsnotify/fsnotify/blob/9342b6df577910c6eac718dc62845d8c95f8548b/backend_inotify.go#L114
        // https://github.com/revel/cmd/blob/4c7ddf5567b1f9facb0dbf9bf4511e2da1934fdb/watcher/watcher.go#L289
        if npFlags & npFlagRemove == 0, npFlags != npFlagStatAttr {
            npFlags &= ~npFlagStatAttr
        }

        // dir-only watcher's events normally have flags=0
        // we *could* send dir change events for explicit file watchers too,
        // but it's unnecessary when we have granular notifications for every file in the dir
        if isDirChange {
            npFlags |= npFlagDirChange
        }

        // TODO: filter out zero flags

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

@_cdecl("swext_fsevents_monitor_dirs")
func swext_fsevents_monitor_dirs() -> UnsafeMutablePointer<CChar> {
    func callback(stream _: ConstFSEventStreamRef, info _: UnsafeMutableRawPointer?, numEvents: Int, paths: UnsafeMutableRawPointer, flags: UnsafePointer<FSEventStreamEventFlags>, ids _: UnsafePointer<FSEventStreamEventId>) {
        let pathsAndFlags = dedupeEvents(paths, flags, numEvents)

        // send to krpc
        let (buf, len) = eventsToKrpc(pathsAndFlags, isDirChange: true)
        swext_fsevents_cb_krpc_events(buf, len)
        buf.deallocate()
    }

    // we must always watch all dirs.
    // otherwise docker bind mounted dir could disappear, then we'd have problems for 2 hours (until cache expiry)
    // not to mention machines
    let paths = ["/"] as CFArray
    let stream = FSEventStreamCreate(nil, callback, nil, paths,
                                     UInt64(kFSEventStreamEventIdSinceNow), dirChangeDebounce,
                                     UInt32(kFSEventStreamCreateFlagIgnoreSelf | kFSEventStreamCreateFlagNoDefer))
    guard let stream else {
        return strdup("FSEventStreamCreate failed")
    }

    // exclude chatty paths
    let homePrefix = FileManager.default.homeDirectoryForCurrentUser.path
    let cachesPrefix = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].path
    let tmpPrefix = FileManager.default.temporaryDirectory.path
    let nfsPrefix = "\(homePrefix)/OrbStack"
    let appDataPrefix = "\(homePrefix)/.orbstack"
    let excludePaths = [cachesPrefix, tmpPrefix, nfsPrefix, appDataPrefix] as CFArray
    guard FSEventStreamSetExclusionPaths(stream, excludePaths) else {
        return strdup("FSEventStreamSetExclusionPaths failed")
    }

    FSEventStreamSetDispatchQueue(stream, fseventsQueue)
    guard FSEventStreamStart(stream) else {
        return strdup("FSEventStreamStart failed")
    }

    // retain for Go
    FSEventStreamRetain(stream)
    return strdup("")
}

// now, the standard files monitor
private class VmNotifier {
    private var stream: FSEventStreamRef?

    init() throws {
        // FSEventStreamCreate fails with no paths
        try newStream(paths: ["/.__non_existent_path__/.xyz"])
    }

    private func newStream(paths: [String], lastEventId: UInt64 = UInt64(kFSEventStreamEventIdSinceNow)) throws {
        func callback(stream _: ConstFSEventStreamRef, info _: UnsafeMutableRawPointer?, numEvents: Int, paths: UnsafeMutableRawPointer, flags: UnsafePointer<FSEventStreamEventFlags>, ids _: UnsafePointer<FSEventStreamEventId>) {
            let pathsAndFlags = dedupeEvents(paths, flags, numEvents)

            // send to krpc
            let (buf, len) = eventsToKrpc(pathsAndFlags, isDirChange: false)
            swext_fsevents_cb_krpc_events(buf, len)
            buf.deallocate()
        }

        let stream = FSEventStreamCreate(nil, callback, nil, paths as CFArray,
                                         lastEventId, fileWatchDebounce,
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
func swext_fsevents_VmNotifier_updatePaths(ptr: UnsafeMutableRawPointer, _ paths: UnsafeMutablePointer<UnsafeMutablePointer<CChar>>, _ numPaths: Int) -> GResultErr {
    var newPaths: [String] = []
    for i in 0 ..< numPaths {
        newPaths.append(String(cString: paths[i]))
    }

    return doGenericErr(ptr) { (notifier: VmNotifier) in
        try notifier.updatePaths(newPaths: newPaths)
    }
}

@_cdecl("swext_fsevents_VmNotifier_start")
func swext_fsevents_VmNotifier_start(ptr: UnsafeMutableRawPointer) -> GResultErr {
    return doGenericErr(ptr) { (notifier: VmNotifier) in
        try notifier.start()
    }
}

@_cdecl("swext_fsevents_VmNotifier_stop")
func swext_fsevents_VmNotifier_stop(ptr: UnsafeMutableRawPointer) {
    let notifier = Unmanaged<VmNotifier>.fromOpaque(ptr).takeUnretainedValue()
    notifier.stop()
}

@_cdecl("swext_fsevents_VmNotifier_finalize")
func swext_fsevents_VmNotifier_finalize(ptr: UnsafeMutableRawPointer) {
    let notifier = Unmanaged<VmNotifier>.fromOpaque(ptr).takeUnretainedValue()
    notifier.stop()
    Unmanaged<VmNotifier>.fromOpaque(ptr).release()
}
