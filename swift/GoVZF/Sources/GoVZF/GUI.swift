//
// Created by Danny Lin on 6/22/23.
//

import CBridge
import Foundation

@_cdecl("swext_gui_run_as_admin")
func swext_gui_run_as_admin(shellScriptC: UnsafePointer<CChar>, promptC: UnsafePointer<CChar>) -> GResultErr {
    let shellScript = String(cString: shellScriptC)
    let prompt = String(cString: promptC)

    let escapedSh = shellScript.replacingOccurrences(of: "\\", with: "\\\\")
        .replacingOccurrences(of: "\"", with: "\\\"")
    let appleScript = "do shell script \"\(escapedSh)\" with administrator privileges with prompt \"\(prompt)\""
    let script = NSAppleScript(source: appleScript)
    guard script != nil else {
        return GResultErr(err: strdup("failed to create script"))
    }

    var error: NSDictionary?
    script?.executeAndReturnError(&error)
    if error != nil {
        return GResultErr(err: strdup(error?[NSAppleScript.errorMessage] as? String ?? "unknown error"))
    }

    return GResultErr(err: nil)
}
