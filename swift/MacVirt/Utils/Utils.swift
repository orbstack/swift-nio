//
// Created by Danny Lin on 2/5/23.
//

import AppKit

// TODO: based on beta
private let KILLSWITCH_EXPIRE_DAYS = -1.0

func processIsTranslated() -> Bool {
    var ret = Int32(0)
    var size = 4
    let result = sysctlbyname("sysctl.proc_translated", &ret, &size, nil, 0)
    if result == -1 {
        return false
    }
    return ret == 1
}

func readKillswitchTime() throws -> NSDate {
    let infoPath = Bundle.main.bundlePath.appending("/Contents/Info.plist")
    if let infoAttr = try? FileManager.default.attributesOfItem(atPath: infoPath),
       let infoDate = infoAttr[FileAttributeKey.creationDate] as? NSDate
    {
        return infoDate
    }

    throw NSError(domain: "MacVirt", code: 1, userInfo: [
        NSLocalizedDescriptionKey: "Failed to read killswitch time",
    ])
}

// not important for security, just UX
func killswitchExpired() -> Bool {
    if KILLSWITCH_EXPIRE_DAYS < 0 {
        return false
    }

    do {
        let killswitchTime = try readKillswitchTime()
        let now = NSDate()
        let diff = now.timeIntervalSince(killswitchTime as Date)
        // 30 days, in seconds
        return diff > 60 * 60 * 24 * KILLSWITCH_EXPIRE_DAYS
    } catch {
        return false
    }
}

struct AppleScriptError: Error {
    let output: String
}

private func escapeShellArg(_ arg: String) -> String {
    return "'" + arg.replacingOccurrences(of: "'", with: "'\\''") + "'"
}

private func escapeShellArgs(_ args: [String]) -> String {
    return args.map(escapeShellArg).joined(separator: " ")
}

func openTerminal(_ command: String, _ args: [String]) async throws {
    NSLog("Run command: \(command) \(args.joined(separator: " "))")

    let terminalBundle = InstalledApps.lastUsedTerminal
    // exception: Alacritty doesn't support opening .sh
    if terminalBundle.id == InstalledApps.alacritty {
        // if already running
        do {
            try await runProcessChecked("\(terminalBundle.url.path)/Contents/MacOS/alacritty", ["msg", "create-window", "-e", command] + args)
        } catch {
            // if not running, open new window
            try await runProcessChecked("/usr/bin/open", ["-n", "-b", terminalBundle.id, "--args", "-e", command] + args)
        }
        return
    }

    // make tmp file
    let tmpDir = FileManager.default.temporaryDirectory
    let uuid = UUID().uuidString.prefix(8)
    let tmpFile = tmpDir.appendingPathComponent("orbstack-open-terminal_\(uuid).sh")

    // write command to tmp file
    // use cleanup function to do escape
    // and to work around Warp not running with "; exit 0", kill ppid. clean exit not needed
    // Warp also sets working dir to script path, so go to home if so
    let command = """
    #!/bin/sh -e
    cleanup() {
        rm -f \(escapeShellArg(tmpFile.path))
        kill -9 $PPID
    }

    if [[ "$PWD" == \(escapeShellArg(tmpDir.path))* ]]; then
        cd ~
    fi

    trap cleanup EXIT
    \(escapeShellArgs([command] + args))
    """
    try command.write(to: tmpFile, atomically: false, encoding: .utf8)

    // make tmp file executable
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: tmpFile.path)

    try await openViaAppleEvent(tmpFile, bundleId: terminalBundle.id) {
        try await runProcessChecked("/usr/bin/open", ["-b", terminalBundle.id])
    }
}

func runAsAdmin(script shellScript: String, prompt: String = "") throws {
    let escapedSh = shellScript.replacingOccurrences(of: "\\", with: "\\\\")
        .replacingOccurrences(of: "\"", with: "\\\"")
    let appleScript = "do shell script \"\(escapedSh)\" with administrator privileges with prompt \"\(prompt)\""
    let script = NSAppleScript(source: appleScript)
    guard script != nil else {
        throw AppleScriptError(output: "failed to create script")
    }

    var error: NSDictionary?
    script?.executeAndReturnError(&error)
    if error != nil {
        throw AppleScriptError(output: error?[NSAppleScript.errorMessage] as? String ?? "unknown error")
    }
}

// Workaround for macOS 13 bug (FB11745075): https://developer.apple.com/forums/thread/723842
// this doesn't require apple event privileges because it's just open
private func openViaAppleEvent(_ url: URL, bundleId: String, openFn: () async throws -> Void) async throws {
    var terminal: NSRunningApplication
    if let existingTerminal = NSRunningApplication.runningApplications(withBundleIdentifier: bundleId).first {
        terminal = existingTerminal
    } else {
        // opening terminal app this way causes it to open a default window, so we get two windows
        // so only open it if not already running
        try await openFn()
        if let newTerminal = NSRunningApplication.runningApplications(withBundleIdentifier: bundleId).first {
            terminal = newTerminal
        } else {
            return
        }
    }

    // Create a 'aevt' / 'odoc' Apple event.
    let target = NSAppleEventDescriptor(processIdentifier: terminal.processIdentifier)
    let event = NSAppleEventDescriptor(
        eventClass: AEEventClass(kCoreEventClass),
        eventID: AEEventID(kAEOpenDocuments),
        targetDescriptor: target,
        returnID: AEReturnID(kAutoGenerateReturnID),
        transactionID: AETransactionID(kAnyTransactionID)
    )

    // Set its direct option to a list containing our script file URL.
    let scriptURL = NSAppleEventDescriptor(fileURL: url)
    let itemsToOpen = NSAppleEventDescriptor.list()
    itemsToOpen.insert(scriptURL, at: 0)
    event.setParam(itemsToOpen, forKeyword: keyDirectObject)

    // Send the Apple event.
    do {
        let reply = try event.sendEvent(options: [.waitForReply], timeout: 30.0)

        // AFAICT there’s no point looking at the reply here.  Terminal
        // doesn’t report errors this way.
        _ = reply

        // If the event was sent successfully, bring Terminal to the front.
        terminal.activate()
    } catch let error as NSError {
        throw error
    }
}

extension String {
    func replaceNSRegex(_ regex: NSRegularExpression, with: (NSTextCheckingResult) -> String) -> (String, Bool) {
        let nsString = self as NSString
        let matches = regex.matches(in: self, options: [], range: NSRange(location: 0, length: nsString.length))
        var result = self
        for match in matches.reversed() {
            let replacement = with(match)
            result = (result as NSString).replacingCharacters(in: match.range, with: replacement)
        }
        return (result, !matches.isEmpty)
    }

    func replaceNSRegex(_ regex: NSRegularExpression, with: String) -> (String, Bool) {
        return replaceNSRegex(regex) { _ in with }
    }
}

enum Folders {
    static let home = FileManager.default.homeDirectoryForCurrentUser.path
    static let nfsName = "OrbStack"
    static let nfs = "\(home)/\(nfsName)"
    static let nfsDocker = "\(nfs)/docker"
    static let nfsDockerVolumes = "\(nfsDocker)/volumes"
    static let nfsDockerImages = "\(nfsDocker)/images"
    static let nfsDockerContainers = "\(nfsDocker)/containers"

    static let appData = "\(home)/.orbstack"
    static let config = "\(appData)/config"
    static let run = "\(appData)/run"
    static let log = "\(appData)/log"
    static let userData = "\(appData)/data"
}

enum Files {
    static let dockerDaemonConfig = "\(Folders.config)/docker.json"
    static let dockerSocket = "\(Folders.run)/docker.sock"
    static let vmgrSocket = "\(Folders.run)/vmcontrol.sock"
    static let sconSocket = "\(Folders.run)/sconrpc.sock"
    static let vmgrLog = "\(Folders.log)/vmgr.log"
    static let guiLog = "\(Folders.log)/gui.log"
    static let installId = "\(Folders.appData)/.installid"

    static let username = NSUserName()
}

enum ContainerIds {
    static let docker = "01GQQVF6C60000000000DOCKER"
    static let k8s = docker
}

enum AppleEvents {
    static func sendReopen(targetDescriptor: NSAppleEventDescriptor) {
        let event = NSAppleEventDescriptor.appleEvent(withEventClass: kCoreEventClass,
                                                      eventID: kAEReopenApplication,
                                                      targetDescriptor: targetDescriptor,
                                                      returnID: AEReturnID(kAutoGenerateReturnID),
                                                      transactionID: AETransactionID(kAnyTransactionID))
        AESendMessage(event.aeDesc, nil, AESendMode(kAENoReply), kAEDefaultTimeout)
    }
}

private let dockerDesktopLastUsedThreshold: TimeInterval = 1 * 30 * 24 * 60 * 60 // 1 month

struct BundleCandidate {
    let bundle: BundleInfo
    let running: Bool
    let timestamp: Date
}

enum InstalledApps {
    // lazy init
    static let dockerDesktopRecentlyUsed = isDockedDesktopRecentlyUsed()
    static func isDockedDesktopRecentlyUsed() -> Bool {
        if let bundleUrl = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.docker.docker") {
            let attributes = NSMetadataItem(url: bundleUrl)
            if let date = attributes?.value(forAttribute: kMDItemLastUsedDate as String) as? Date {
                return abs(date.timeIntervalSinceNow) < dockerDesktopLastUsedThreshold
            }
        }
        return false
    }

    // special case: Alacritty doesn't support opening .sh, and doesn't declare Shell
    static let alacritty = "org.alacritty"

    private static let extraTerminalBundleIds = [
        alacritty,
    ]

    // cached: lookup takes ~50 ms
    static let lastUsedTerminal = selectTerminal()
    static func selectTerminal() -> BundleInfo {
        // (Shell, public.unix-executable) is the most reliable type:
        // kitty = Editor for *.sh; Shell for public.unix-executable
        // iTerm = Editor for *.sh; Shell for public.unix-executable
        // Warp = Shell for com.apple.terminal.shell-script; Shell for public.unix-executable
        // WezTerm = Editor for *.sh; Shell for public.unix-executable
        // Hyper = only Shell for public.unix-executable
        // Ghostty = Editor for *.sh; Shell for public.unix-executable
        // VS Code = Editor for *.sh
        let execUrl = Bundle.main.executableURL!
        var appUrls = LSCopyApplicationURLsForURL(execUrl as CFURL, .shell)?.takeRetainedValue() as? [URL] ?? []

        // find extra terminals
        for bundleId in extraTerminalBundleIds {
            if let bundleUrl = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleId) {
                appUrls.append(bundleUrl)
            }
        }

        return appUrls
            .compactMap { bundleUrl in
                guard let attributes = NSMetadataItem(url: bundleUrl),
                      let bundleId = attributes.value(forAttribute: kMDItemCFBundleIdentifier as String) as? String
                      else {
                    // to help type inference
                    return BundleCandidate?(nil)
                }

                if let runningApp = NSRunningApplication.runningApplications(withBundleIdentifier: bundleId).first,
                   let launchDate = runningApp.launchDate
                {
                    return BundleCandidate(bundle: BundleInfo(id: bundleId, url: bundleUrl), running: true, timestamp: launchDate)
                }

                if let date = attributes.value(forAttribute: kMDItemLastUsedDate as String) as? Date {
                    return BundleCandidate(bundle: BundleInfo(id: bundleId, url: bundleUrl), running: false, timestamp: date)
                }

                return nil
            }
            // sort by running first, then by last used
            .sorted { a, b in
                if a.running != b.running {
                    return a.running
                }
                return a.timestamp > b.timestamp
            }
            .first?.bundle ?? BundleInfo(id: "com.apple.Terminal", url: URL(fileURLWithPath: ""))
    }
}

struct BundleInfo {
    let id: String
    let url: URL
}

enum Users {
    static let gidAdmin: gid_t = 80

    // lazy init
    static let hasAdmin = _hasAdmin()
    private static func _hasAdmin() -> Bool {
        // user supplementary groups is fast, opendirectory is slow
        var gids = [gid_t](repeating: 0, count: 100)
        // don't bother to slice - the rest are 0
        if getgroups(100, &gids) < 0 {
            return false
        }
        return gids.contains(gidAdmin)
    }
}

enum K8sConstants {
    static let context = "orbstack"
    static let apiResId = K8SResourceId.service(namespace: "default", name: "kubernetes")
}
