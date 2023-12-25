//
//  Uninstaller.swift
//  SwiftAuthorizationSample
//
//  Created by Josh Kaplan on 2021-10-24
//

import Foundation

/// A self uninstaller which performs the logical equivalent of the non-existent `SMJobUnbless`.
///
/// Because Apple does not provide an API to perform an "unbless" operation, the technique used here relies on a few key behaviors:
///  - To deregister the helper tool with launchd, the `launchctl` command line utility which ships with macOS is used
///     - The `unload` command used is publicly documented
///  - An assumption that this helper tool when installed is located at `/Library/PrivilegedHelperTools/<helper_tool_name>`
///     - While this location is not documented in `SMJobBless`, it is used in Apple's EvenBetterAuthorizationSample `Uninstall.sh` script
///     - This is used to determine if this helper tool is in fact running from the blessed location
///  - To remove the `launchd` property list, its location is assumed to be `/Library/LaunchDaemons/<helper_tool_name>.plist`
///     - While this location is not documented in `SMJobBless`, it is used in Apple's EvenBetterAuthorizationSample `Uninstall.sh` script
enum Uninstaller {
    enum UninstallError: Error {
        case launchctlFailure(statusCode: Int32)
        case notProcessId(invalidArgument: String)
    }

    static let cliCommand = "uninstall"

    /// Indirectly uninstalls this helper tool. Calling this function will terminate this process unless an error is throw.
    ///
    /// Uninstalls this helper tool by relaunching itself not via XPC such that the installation can occur succesfully.
    ///
    /// - Throws: If unable to determine the on disk location of this running code.
    static func uninstallFromXPC() throws {
        activityTracker.begin()
        defer { activityTracker.end() }

        NSLog("starting uninstaller")
        let process = Process()
        process.launchPath = try CodeInfo.currentCodeLocation().path
        process.qualityOfService = .utility
        process.arguments = [cliCommand, String(getpid())]
        process.launch()
        exit(0)
    }

    static func uninstallFromCli(withArguments arguments: [String]) throws -> Never {
        if arguments.count == 1 {
            try uninstallNow()
        } else {
            guard let pid: pid_t = Int32(arguments[1]) else {
                throw UninstallError.notProcessId(invalidArgument: arguments[1])
            }
            try waitPidExitAndUninstall(pid: pid)
        }
    }

    private static func waitPidExitAndUninstall(pid: pid_t) throws -> Never {
        // When passing 0 as the second argument, no signal is sent, but existence and permission checks are still
        // performed. This checks for the existence of a process ID. If 0 is returned the process still exists, so loop
        // until 0 is no longer returned.
        while kill(pid, 0) == 0 { // in practice this condition almost always evaluates to false on its first check
            usleep(50 * 1000) // sleep for 50ms
            NSLog("waiting for parent \(pid) to exit")
        }
        NSLog("parent exited, uninstalling")

        try uninstallNow()
    }

    /// Uninstalls this helper tool.
    ///
    /// This function will not work if called when this helper tool was started by an XPC call because `launchctl` will be unable to unload.
    ///
    /// If the uninstall fails when deleting either the `launchd` property list for this executable or the on disk representation of this helper tool then the uninstall
    /// will be an incomplete state; however, it will no longer be started by `launchd` (and in turn not accessible via XPC) and so will be mostly uninstalled even
    /// though some on disk portions will remain.
    ///
    /// - Throws: Due to one of: unable to determine the on disk location of this running code, that location is not the blessed location, `launchctl` can't
    /// unload this helper tool, the `launchd` property list for this helper tool can't be deleted, or the on disk representation of this helper tool can't be deleted.
    private static func uninstallNow() throws -> Never {
        // Equivalent to: launchctl unload /Library/LaunchDaemons/<helper tool name>.plist
        NSLog("unload from launchd")
        let process = Process()
        process.launchPath = "/bin/launchctl"
        process.qualityOfService = .utility
        process.arguments = ["unload", PHShared.installedPlistURL.path]
        process.launch()
        process.waitUntilExit()
        let status = process.terminationStatus
        guard status == 0 else {
            throw UninstallError.launchctlFailure(statusCode: status)
        }

        NSLog("delete files")
        try FileManager.default.removeItem(at: PHShared.installedPlistURL)
        try FileManager.default.removeItem(at: PHShared.installedURL)
        NSLog("uninstalled, exiting")
        exit(0)
    }
}
