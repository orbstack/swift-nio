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

    static func mergeX(a: NSImage, b: NSImage, xPadding: CGFloat = 0) -> NSImage {
        let width = a.size.width + b.size.width + xPadding
        let height = max(a.size.height, b.size.height)
        let size = NSSize(width: width, height: height)
        let image = NSImage(size: size)
        image.lockFocus()
        // vertically center
        let aRect = NSRect(x: 0, y: (height - a.size.height) / 2, width: a.size.width, height: a.size.height)
        let bRect = NSRect(x: a.size.width + xPadding, y: (height - b.size.height) / 2, width: b.size.width, height: b.size.height)
        a.draw(in: aRect)
        b.draw(in: bRect)
        image.unlockFocus()
        return image
    }
}