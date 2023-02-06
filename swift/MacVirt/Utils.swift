//
// Created by Danny Lin on 2/5/23.
//

import Foundation

func processIsTranslated() -> Bool {
    var ret = Int32(0)
    var size = 4
    let result = sysctlbyname("sysctl.proc_translated", &ret, &size, nil, 0)
    if result == -1 {
        return false
    }
    return ret == 1
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
    \(command) \(args.joined(separator: " "))
    """
    try command.write(to: tmpFileURL, atomically: true, encoding: .utf8)

    // make tmp file executable
    try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: tmpFileURL.path)

    // try iterm2
    do {
        try await runProcessChecked("/usr/bin/open", ["-a", "iTerm", tmpFileURL.path])
    } catch {
        // try terminal
        try await runProcessChecked("/usr/bin/open", ["-a", "Terminal", tmpFileURL.path])
    }

    // get our PATH
    // read environment variables
    let env = ProcessInfo.processInfo.environment
    print("env",env)

    // write it to the tmp file
    let txt=env.map { "\($0.key)=\($0.value)" }.joined(separator: "\n")
    try txt.write(to: tmpFileURL, atomically: true, encoding: .utf8)
}

func runAsAdmin(_ command: String, _ args: [String]) async throws {
    let script = NSAppleScript(source: "do shell script \"\(command) \(args.joined(separator: " "))\" with administrator privileges")
    guard script != nil else {
        throw AppleScriptError(output: "failed to create apple script")
    }

    var error: NSDictionary?
    script?.executeAndReturnError(&error)
    print("error", error ?? "nil")
    if error != nil {
        throw AppleScriptError(output: error?[NSAppleScript.errorMessage] as? String ?? "unknown error")
    }
}