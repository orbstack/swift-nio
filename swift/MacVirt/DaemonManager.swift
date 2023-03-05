//
// Created by Danny Lin on 3/5/23.
//

import Foundation

class DaemonManager {
    func isRunning() async -> Bool {
        // read pid file
        let path = getConfigDir() + "/run/vmgr.pid"
        do {
            let pidStr = try String(contentsOfFile: path)
            let pid = Int(pidStr.trimmingCharacters(in: .whitespacesAndNewlines))
            guard pid != nil else {
                return false
            }

            // check if process is running by sending signal 0
            let ret = kill(pid_t(pid!), 0)
            return ret == 0
        } catch {
            // failed to read pid file
            return false
        }
    }
}