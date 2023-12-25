import Foundation

enum HelperServer {
    static func symlink(req: PHSymlinkRequest) throws {
        activityTracker.begin()
        defer { activityTracker.end() }

        NSLog("symlink: \(req.src) -> \(req.dest)")

        // security: only allow dest to /usr/local/bin/* and /var/run/docker.sock
        guard req.dest == "/var/run/docker.sock" ||
            (req.dest.starts(with: "/usr/local/bin/") && !req.dest.contains(".."))
        else {
            throw PHSymlinkError.pathNotAllowed
        }

        // skip if already exists with correct dest
        do {
            let oldSrc = try FileManager.default.destinationOfSymbolicLink(atPath: req.dest)
            if oldSrc == req.src {
                return
            }
        } catch {
            // doesn't exist
        }

        do {
            // delete the old one
            try FileManager.default.removeItem(atPath: req.dest)
        } catch CocoaError.fileNoSuchFile {
            // doesn't exist
        }

        // create dir (mkdir -p)
        let destDir = URL(fileURLWithPath: req.dest).deletingLastPathComponent().path
        do {
            try FileManager.default.createDirectory(atPath: destDir, withIntermediateDirectories: true, attributes: nil)
            try FileManager.default.createSymbolicLink(atPath: req.dest, withDestinationPath: req.src)
        } catch CocoaError.fileWriteFileExists {
            // already exists
            // probably raced with another setup instance somehow? (app vs. cli background)
        }
    }
}
