import Foundation


func swext_ipc_notify_uievent(eventJsonStr: UnsafePointer<CChar>) {
    let nc = DistributedNotificationCenter.default()
    // deliverImmediately sends even if GUI is in background
    let eventJson = String(cString: eventJsonStr)
    nc.postNotificationName(.init("\(getuid()).dev.orbstack.vmgr.private.UIEvent.test"),
            object: nil,
            userInfo: ["event": eventJson],
            deliverImmediately: true)
}

// iterate through 1K - 64M and send diff message sizes in increments of 1K
// for i in 1...64000 {
//     let size = i * 1024 * 1024
//     let msg = String(repeating: "a", count: size)
//     print("send \(size)")
//     swext_ipc_notify_uievent(eventJsonStr: msg)
//     // sleep 1 ms
//     usleep(1000)
// }

for i in 1...10 {
    let size = i % 2 == 0 ? 1024 : 1024 * 1024
    let msg = String(repeating: "a", count: size)
    print("send \(size)")
    swext_ipc_notify_uievent(eventJsonStr: msg)
    // usleep(1000)
}
