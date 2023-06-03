//
//  Macvlan.swift
//  GoVZF
//
//  Created by Danny Lin on 6/2/23.
//

import Foundation

private let maxMacvlanInterfaces = 128

// serialied by vmnetQueue barriers
class MacvlanRouter {
    private var interfaces = [BridgeNetwork?](repeating: nil, count: maxMacvlanInterfaces)


}
