//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 11/19/24.
//

@main
struct MacVirtApp {
    static func main() {
        if #available(macOS 14, *) {
            MacVirtApp14.main()
        } else {
            MacVirtApp13.main()
        }
    }
}
