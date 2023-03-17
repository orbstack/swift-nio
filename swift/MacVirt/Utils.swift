//
// Created by Danny Lin on 2/5/23.
//

import Cocoa

fileprivate let KILLSWITCH_EXPIRE_DAYS = 30.0

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
        NSLocalizedDescriptionKey: "Failed to read killswitch time"
    ])
}

// not important for security, just UX
func killswitchExpired() -> Bool {
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

struct ProcessResult {
    let output: String
    let status: Int32
}

struct ProcessError: Error {
    let status: Int32
    let output: String
}

struct AppleScriptError: Error {
    let output: String
}

func runProcess(_ command: String, _ args: [String]) async throws -> ProcessResult {
    let task = Process()
    task.launchPath = command
    task.arguments = args

    let outPipe = Pipe()
    task.standardOutput = outPipe
    task.standardError = outPipe
    task.arguments = args
    let readOutputTask = Task.detached {
        let output = String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)!
        return output
    }
    return try await withCheckedThrowingContinuation { continuation in
        task.terminationHandler = { process in
            let status = process.terminationStatus
            Task {
                continuation.resume(returning: ProcessResult(
                    output: await readOutputTask.value,
                    status: status
                ))
            }
        }

        do {
            try task.run()
        } catch {
            continuation.resume(throwing: error)
        }
    }
}

@discardableResult
func runProcessChecked(_ command: String, _ args: [String]) async throws -> String {
    let result = try await runProcess(command, args)
    if result.status != 0 {
        throw ProcessError(status: result.status, output: result.output)
    }
    return result.output
}

func openTerminal(_ command: String, _ args: [String]) async throws {
    // make tmp file
    let tmpDir = FileManager.default.temporaryDirectory
    let tmpFile = tmpDir.appendingPathComponent(UUID().uuidString)
    let tmpFileURL = URL(fileURLWithPath: tmpFile.path)

    // write command to tmp file
    let command = """
    #!/bin/sh -e
    rm -f "\(tmpFileURL.path)"
    "\(command)" \(args.joined(separator: " "))
    """
    try command.write(to: tmpFileURL, atomically: true, encoding: .utf8)

    // make tmp file executable
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: tmpFileURL.path)

    // try iterm2
    do {
        try await runProcessChecked("/usr/bin/open", ["-a", "iTerm"])
        try openViaAppleEvent(tmpFileURL, bundleId: "com.googlecode.iterm2")
    } catch {
        // try terminal
        try await runProcessChecked("/usr/bin/open", ["-a", "Terminal"])
        try openViaAppleEvent(tmpFileURL, bundleId: "com.apple.Terminal")
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
private func openViaAppleEvent(_ url: URL, bundleId: String) throws {
    guard let terminal = NSRunningApplication.runningApplications(withBundleIdentifier: bundleId).first else {
        return
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
