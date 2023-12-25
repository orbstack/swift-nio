//
// Created by Danny Lin on 9/15/23.
//

import Foundation
import SwiftUI

private class CursorView: NSView {
    var cursor: NSCursor = .arrow

    override func resetCursorRects() {
        addCursorRect(bounds, cursor: cursor)
    }
}

private struct CursorRegion: NSViewRepresentable {
    let cursor: NSCursor

    func makeNSView(context _: Context) -> CursorView {
        return CursorView(frame: .zero)
    }

    func updateNSView(_ nsView: CursorView, context _: Context) {
        nsView.cursor = cursor
    }
}

extension View {
    // more reliable than NSCursor.push/.pop
    func cursorRect(_ cursor: NSCursor) -> some View {
        overlay(CursorRegion(cursor: cursor))
    }
}
