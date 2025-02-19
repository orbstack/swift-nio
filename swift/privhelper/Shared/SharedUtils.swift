//
// Created by Danny Lin on 8/15/23.
//

import Foundation

struct ProcessResult {
    let status: Int32
    let stdout: String
    let stderr: String
}

struct ProcessError: Error {
    let status: Int32
    let stderr: String
}

func runProcess(_ command: String, _ args: [String], env: [String: String] = [:]) async throws
    -> ProcessResult
{
    let task = Process()
    task.launchPath = command
    task.arguments = args

    // based on current env, apply overrides
    var newEnv = ProcessInfo.processInfo.environment
    for (key, value) in env {
        newEnv[key] = value
    }
    task.environment = newEnv

    let outPipe = Pipe()
    // "If file is an NSPipe object, launching the receiver automatically closes the write end of the pipe in the current task."
    task.standardOutput = outPipe
    let stdoutReadTask = Task.detached {
        return String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)!
    }

    let errPipe = Pipe()
    task.standardError = errPipe
    let stderrReadTask = Task.detached {
        return String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)!
    }

    return try await withCheckedThrowingContinuation { continuation in
        task.terminationHandler = { process in
            let status = process.terminationStatus
            Task {
                continuation.resume(
                    returning: ProcessResult(
                        status: status,
                        stdout: await stdoutReadTask.value,
                        stderr: await stderrReadTask.value
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
func runProcessChecked(_ command: String, _ args: [String], env: [String: String] = [:])
    async throws -> String
{
    let result = try await runProcess(command, args, env: env)
    if result.status != 0 {
        throw ProcessError(status: result.status, stderr: result.stderr)
    }
    return result.stdout
}
