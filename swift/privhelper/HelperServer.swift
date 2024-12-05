import Foundation

private func retryEintr(_ fn: () -> Int32) -> Int32 {
    while true {
        let ret = fn()
        if ret == -1 && errno == EINTR {
            continue
        }
        return ret
    }
}

enum HelperServer {
    static func symlink(req: PHSymlinkRequest) throws {
        activityTracker.begin()
        defer { activityTracker.end() }

        NSLog("symlink: \(req.src) -> \(req.dest)")

        // security: only allow dest to /usr/local/bin/* and /var/run/docker.sock
        guard
            req.dest == "/var/run/docker.sock"
                || (req.dest.starts(with: "/usr/local/bin/") && !req.dest.contains(".."))
        else {
            throw PHSymlinkError.pathNotAllowed
        }

        // delete the old link
        _ = retryEintr { unlink(req.dest) }

        // create parent dir (no need for recursive)
        let destDir = (req.dest as NSString).deletingLastPathComponent
        // EEXIST is ok: already existed, or we raced with another setup instance
        // any other error = failed to create. we'll find out below when symlink fails with ENOENT
        _ = retryEintr { mkdir(destDir, 0o755) }

        // finally, create the link
        let ret = retryEintr { unistd.symlink(req.src, req.dest) }
        // EEXIST is ok: we raced with another setup instance
        if ret == -1 && errno != EEXIST {
            throw PHSymlinkError.linkError(String(cString: strerror(errno)))
        }
    }
}
