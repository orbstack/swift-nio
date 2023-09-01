//
// Created by Danny Lin on 2/8/23.
//

import Foundation

infix operator %%

extension Int {
    static func %% (_ left: Int, _ right: Int) -> Int {
        if left >= 0 { return left % right }
        if left >= -right { return (left+right) }
        return ((left % right)+right)%right
    }
}

extension String {
    // for macOS < 13
    func deletingPrefix(_ prefix: String) -> String {
        guard hasPrefix(prefix) else {
            return self
        }
        return String(dropFirst(prefix.count))
    }

    func toURL() -> URL? {
        URL(string: self)
    }
}