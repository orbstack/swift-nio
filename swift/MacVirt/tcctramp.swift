//
//  tcctramp.swift
//  MacVirt
//
//  Created by Danny Lin on 12/4/24.
//

import Foundation
import MachO

/*
 Trampoline to launch vmgr with GUI's identity for TCC responsibility/attribution.
 To make this secure, GUI has the "Network Extensions" restricted entitlement despite not needing it, because it ties our binary to the app bundle (by requiring embedded.provisionprofile to match) in order to make sure that malicious callers can't moev our binary somewhere else and trick us into execing some random binary with our TCC identity.
 If we're tied to our containing bundle (via embedded.provisionprofile), and our bundle is sealed (modifying OrbStack Helper.app will cause a Gatekeeper failure because it invalidates bundle resource signatures), then we can be (reasonably) sure that we'll only exec a genuine vmgr binary here.
 */
func tcctrampMain() {
    // technically, all of this is subject to races, e.g. if a malicious caller runs us via a symlink.
    // but there's only so much we can do...

    // get the executable path (not symlinked bundle path)
    var buf = [CChar](repeating: 0, count: Int(MAXPATHLEN))
    var bufSize = UInt32(buf.count)
    let success = _NSGetExecutablePath(&buf, &bufSize) >= 0
    if !success {
        buf = [CChar](repeating: 0, count: Int(bufSize))
        let success2 = _NSGetExecutablePath(&buf, &bufSize) >= 0
        guard success2 else { fatalError("_NSGetExecutablePath failed") }
    }
    var exeUrl = URL(fileURLWithFileSystemRepresentation: buf, isDirectory: false, relativeTo: nil)

    // resolve symlinks
    exeUrl.resolveSymlinksInPath()
    // get exe dir
    exeUrl.deleteLastPathComponent()
    // ../
    exeUrl.deleteLastPathComponent()
    exeUrl.append(path: "Frameworks/OrbStack Helper.app/Contents/MacOS/OrbStack Helper")

    exeUrl.withUnsafeFileSystemRepresentation { vmgrPathC in
        // will not actually be mutated
        CommandLine.unsafeArgv[0] = UnsafeMutablePointer(mutating: vmgrPathC)
        let ret = execv(vmgrPathC, CommandLine.unsafeArgv)
        if ret != 0 {
            perror("execv failed")
            fatalError()
        }
    }
}
