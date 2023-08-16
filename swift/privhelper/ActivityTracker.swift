//
// Created by Danny Lin on 8/16/23.
//

import Foundation

let activityTracker = ActivityTracker()
private let idleDelay: TimeInterval = 15 // seconds

private class FuncDebounce {
    private var timer: DispatchSourceTimer?
    private let delay: TimeInterval
    var action: () -> Void

    init(delay: TimeInterval, action: @escaping () -> Void) {
        self.delay = delay
        self.action = action
    }

    func call() {
        timer?.cancel()

        let newTimer = DispatchSource.makeTimerSource()
        newTimer.setEventHandler { [self] in
            self.action()
        }
        newTimer.schedule(deadline: .now() + delay, leeway: .seconds(1))
        newTimer.activate()
        timer = newTimer
    }
}

class ActivityTracker {
    // refcount
    private let lock = NSLock()
    private var activityCount = 0
    private let debounce: FuncDebounce

    init() {
        // can't set self closure until fields inited
        debounce = FuncDebounce(delay: idleDelay) { }
        debounce.action = onIdle
    }

    func begin() {
        lock.lock()
        defer { lock.unlock() }

        activityCount += 1
        debounce.call()
    }

    func end() {
        lock.lock()
        defer { lock.unlock() }

        activityCount -= 1
        debounce.call()
    }

    func kick() {
        lock.lock()
        defer { lock.unlock() }

        debounce.call()
    }

    private func onIdle() {
        lock.lock()
        defer { lock.unlock() }

        if activityCount == 0 {
            NSLog("idle, exiting")
            exit(0)
        }
    }
}