//
// Created by Danny Lin on 5/21/23.
//

import AppKit
import Foundation

extension ContainerRecord {
    func openInTerminal() async {
        do {
            try await openTerminal(AppConfig.shellExe, ["-m", name])
        } catch {
            NSLog("Open terminal failed: \(error)")
        }
    }

    func openNfsDirectory() {
        NSWorkspace.openFolder("\(Folders.nfs)/\(name)")
    }
}
