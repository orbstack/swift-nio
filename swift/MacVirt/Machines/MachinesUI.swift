//
// Created by Danny Lin on 5/21/23.
//

import Foundation
import AppKit

extension ContainerRecord {
    func openInTerminal() async {
        do {
            try await openTerminal(AppConfig.shellExe, ["-m", name])
        } catch {
            NSLog("Open terminal failed: \(error)")
        }
    }

    func openNfsDirectory() {
        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: "\(Folders.nfs)/\(name)")    }
}