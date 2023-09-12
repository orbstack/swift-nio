import Foundation

let nc = DistributedNotificationCenter.default()
// TODO security
nc.addObserver(forName: .init("\(getuid()).dev.orbstack.vmgr.private.UIEvent.test"), object: nil, queue: nil) { notification in
    guard let eventJson = notification.userInfo?["event"] as? String else {
        NSLog("Notification is missing data: \(notification)")
        return
    }
    let len = eventJson.count
    // verify: entire message content is 'a'
    // let expected = String(repeating: "a", count: len)
    // if eventJson != expected {
    //     NSLog("Notification data is incorrect: \(notification)")
    //     return
    // }
    print("recv \(len)")
}

RunLoop.main.run()
