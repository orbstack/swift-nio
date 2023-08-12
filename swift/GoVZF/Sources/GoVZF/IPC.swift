//
// Created by Danny Lin on 8/11/23.
//

import Foundation

@_cdecl("swext_ipc_notify_started")
func swext_ipc_notify_started() {
    let nc = DistributedNotificationCenter.default()
    // deliverImmediately sends even if GUI is in background
    nc.postNotificationName(.init("dev.orbstack.vmgr.private.DaemonStarted"),
            object: nil,
            userInfo: ["pid": getpid()],
            deliverImmediately: true)
}

@_cdecl("swext_ipc_notify_docker_event")
func swext_ipc_notify_docker_event(eventJsonStr: UnsafePointer<CChar>) {
    let nc = DistributedNotificationCenter.default()
    // deliverImmediately for meneu bar app
    let eventJson = String(cString: eventJsonStr)
    nc.postNotificationName(.init("dev.orbstack.vmgr.private.DockerUIEvent"),
            object: nil,
            userInfo: ["event_json": eventJson],
            deliverImmediately: true)
}

@_cdecl("swext_ipc_notify_drm_warning")
func swext_ipc_notify_drm_warning(eventJsonStr: UnsafePointer<CChar>) {
    let nc = DistributedNotificationCenter.default()
    // deliverImmediately: important
    let eventJson = String(cString: eventJsonStr)
    nc.postNotificationName(.init("dev.orbstack.vmgr.private.DRMWarning"),
            object: nil,
            userInfo: ["event_json": eventJson],
            deliverImmediately: true)
}
