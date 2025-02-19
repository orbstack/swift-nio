//
// Created by Danny Lin on 5/21/23.
//

import AppKit
import Foundation

extension ContainerRecord {
    func openInTerminal() async {
        do {
            // -w="" opens terminal in machine's home dir
            try await openTerminal(AppConfig.shellExe, ["-m", name, "-w", ""])
        } catch {
            NSLog("Open terminal failed: \(error)")
        }
    }

    func openNfsDirectory() {
        NSWorkspace.openFolder("\(Folders.nfs)/\(name)")
    }
}
