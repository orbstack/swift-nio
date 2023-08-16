import Foundation

struct HelperServer {
    static func symlink(req: PHSymlinkRequest) throws {
        NSLog("symlink: \(req.src) -> \(req.dest)")

        // security: only allow dest to /usr/local/bin/* and /var/run/docker.sock
        guard req.dest == "/var/run/docker.sock" ||
                      (req.dest.starts(with: "/usr/local/bin/") && !req.dest.contains("/") && !req.dest.contains("..")) else {
            throw PHSymlinkError.pathNotAllowed
        }

        // skip if already exists with correct dest
        var oldSrc: String?
        do {
            oldSrc = try FileManager.default.destinationOfSymbolicLink(atPath: req.dest)
        } catch {
            // doesn't exist
        }
        if oldSrc == req.src {
            return
        }

        // security: don't overwrite existing /var/run/docker.sock if it points to another user's OrbStack sock
        if req.dest == "/var/run/docker.sock",
           let oldSrc,
           oldSrc.contains("/.orbstack/") {
            throw PHSymlinkError.existingSocketLink
        }

        // create dir (mkdir -p)
        let destDir = URL(fileURLWithPath: req.dest).deletingLastPathComponent().path
        try FileManager.default.createDirectory(atPath: destDir, withIntermediateDirectories: true, attributes: nil)

        try FileManager.default.createSymbolicLink(atPath: req.dest, withDestinationPath: req.src)
    }
}
