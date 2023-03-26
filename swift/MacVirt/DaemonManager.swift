//
// Created by Danny Lin on 3/5/23.
//

import Foundation

class DaemonManager {
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

    func isRunning() -> Bool {
        // no point in using kill(pid, 0) test. flock is already atomic
        return getPid() != nil
    }
}