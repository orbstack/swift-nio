//
//  Utils.swift
//  guihelper
//
//  Created by Danny Lin on 2/20/23.
//

import Foundation

struct AppleScriptError: Error {
    let output: String
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
