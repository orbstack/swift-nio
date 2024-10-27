//
//  VersionGate.swift
//  MacVirt
//
//  Created by Danny Lin on 10/26/24.
//

import AppKit

/*
 * macOS 15.0 beta 1–4 has a mismatching ABI for actor-isolated Swift Concurrency APIs.
 * The new isolated APIs are used when compiling with Xcode 16+ (swiftc 6.0+).
 *
 * Betas are considered 15.0 for @backDeployed version checks, causing the mismatching ABI to be used.
 * This causes segfaults in runProcess, JsonRPCClient (via AsyncHTTPClient), and more.
 */
private let badMacOS15Betas = [
    "24A5264n",  // 15.0 beta 1
    "24A5279h",  // 15.0 beta 2
    "24A5289g",  // 15.0 beta 3
    "24A5289h",  // 15.0 beta 3 hotfix
    "24A5298h",  // 15.0 beta 4
]

enum VersionGate {
    static func maybeShowMacOS15BetaAlert() {
        // macOS 15.0.0
        let osVersion = ProcessInfo().operatingSystemVersion
        if osVersion.majorVersion != 15 || osVersion.minorVersion != 0
            || osVersion.patchVersion != 0
        {
            return
        }

        // check for beta 1–4 build number
        withUnsafeTemporaryAllocation(of: UInt8.self, capacity: 64) { buf in
            var size = 64
            let ret = sysctlbyname("kern.osversion", buf.baseAddress, &size, nil, 0)
            if ret != 0 {
                fatalError("sysctlbyname failed: \(ret)")
            }

            let buildNumber = String(cString: buf.baseAddress!)
            if badMacOS15Betas.contains(buildNumber) {
                // Apple uses
                let alert = NSAlert()
                alert.messageText = "A macOS update is required."
                alert.informativeText =
                    "macOS 15 beta 1–4 has a bug that prevents OrbStack from working. Please update to stable macOS 15 or newer."
                alert.addButton(withTitle: "Update")
                alert.addButton(withTitle: "Quit")

                // open Settings > Software Update
                if alert.runModal() == .alertFirstButtonReturn,
                    let url = URL(
                        string:
                            "x-apple.systempreferences:com.apple.Software-Update-Settings.extension"
                    )
                {
                    NSWorkspace.shared.open(url)
                }

                exit(1)
            }
        }
    }
}
