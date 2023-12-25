//
// Created by Danny Lin on 8/11/23.
//

import Foundation

@_cdecl("swext_ipc_notify_uievent")
func swext_ipc_notify_uievent(eventJsonStr: UnsafePointer<CChar>) {
    let nc = DistributedNotificationCenter.default()
    // deliverImmediately sends even if GUI is in background
    let eventJson = String(cString: eventJsonStr)
    nc.postNotificationName(.init("\(getuid()).dev.orbstack.vmgr.private.UIEvent"),
                            object: nil,
                            userInfo: ["event": eventJson],
                            deliverImmediately: true)
}
