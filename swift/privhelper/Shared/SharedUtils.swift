//
// Created by Danny Lin on 8/15/23.
//

import Foundation

struct ProcessResult {
    let output: String
    let status: Int32
}

struct ProcessError: Error {
    let status: Int32
    let output: String
}

func runProcess(_ command: String, _ args: [String], env: [String: String] = [:]) async throws -> ProcessResult {
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
    task.standardOutput = outPipe
    task.standardError = outPipe
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
func runProcessChecked(_ command: String, _ args: [String], env: [String: String] = [:]) async throws -> String {
    let result = try await runProcess(command, args, env: env)
    if result.status != 0 {
        throw ProcessError(status: result.status, output: result.output)
    }
    return result.output
}
