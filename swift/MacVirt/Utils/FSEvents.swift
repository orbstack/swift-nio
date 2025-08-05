import Foundation

private enum FSEventsError: Error {
    case createStreamFailed
    case startFailed
}

private func cFsEventsCallback(stream: FSEventStreamRef, info: UnsafeMutableRawPointer?, numEvents: Int, paths: UnsafeMutableRawPointer, flags: UnsafePointer<FSEventStreamEventFlags>, ids: UnsafePointer<FSEventStreamEventId>) {
    guard let info else {
        NSLog("FSEventsListener: no info!")
        return
    }

    let callback = Unmanaged<FSEventsListener>.fromOpaque(info).takeUnretainedValue().callback
    let paths = paths.assumingMemoryBound(to: UnsafePointer<CChar>.self)
    let events = (0..<numEvents).map { i in
        FsEvent(path: String(cString: paths[i]), flags: flags[i], id: ids[i])
    }
    callback(events)
}

struct FsEvent {
    let path: String
    let flags: FSEventStreamEventFlags
    let id: FSEventStreamEventId
}

class FSEventsListener {
    typealias Callback = ([FsEvent]) -> Void

    private var stream: FSEventStreamRef! = nil
    fileprivate let callback: Callback

    init(paths: [String], flags: FSEventStreamEventFlags, latency: TimeInterval, callback: @escaping Callback) throws {
        self.callback = callback
        var context = FSEventStreamContext(version: 0, info: Unmanaged.passUnretained(self).toOpaque(), retain: nil, release: nil, copyDescription: nil)
        guard let stream = FSEventStreamCreate(nil, cFsEventsCallback, &context, paths as CFArray, UInt64(kFSEventStreamEventIdSinceNow), latency, .zero) else {
            throw FSEventsError.createStreamFailed
        }
        self.stream = stream

        FSEventStreamSetDispatchQueue(stream, DispatchQueue.main)
        guard FSEventStreamStart(stream) else {
            FSEventStreamInvalidate(stream)
            FSEventStreamRelease(stream)
            throw FSEventsError.startFailed
        }
    }

    deinit {
        FSEventStreamStop(stream)
        FSEventStreamInvalidate(stream)
        FSEventStreamRelease(stream)
    }
}
