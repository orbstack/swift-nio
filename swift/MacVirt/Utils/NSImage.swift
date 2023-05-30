//
// Created by Danny Lin on 5/29/23.
//

import Foundation
import AppKit

// https://stackoverflow.com/questions/45028530/set-image-color-of-a-template-image/64049177#64049177
extension NSImage {
    func tint(color: NSColor) -> NSImage {
        return NSImage(size: size, flipped: false) { (rect) -> Bool in
            color.set()
            rect.fill()
            self.draw(in: rect, from: NSRect(origin: .zero, size: self.size), operation: .destinationIn, fraction: 1.0)
            return true
        }
    }
}