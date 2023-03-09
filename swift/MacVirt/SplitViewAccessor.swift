//
// Created by Danny Lin on 3/8/23.
//

import Foundation
import SwiftUI

struct SplitViewAccessor: NSViewRepresentable {
    @Binding var sideCollapsed: Bool

    func makeNSView(context: Context) -> some NSView {
        let view = MyView()
        view.sideCollapsed = _sideCollapsed
        return view
    }

    func updateNSView(_ nsView: NSViewType, context: Context) {
    }

    class MyView: NSView {
        var sideCollapsed: Binding<Bool>?
        private var hasSetValue = false

        weak private var controller: NSSplitViewController?
        private var observer: Any?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            var sview = self.superview

            // find split view through hierarchy
            while sview != nil, !sview!.isKind(of: NSSplitView.self) {
                sview = sview?.superview
            }
            guard let sview = sview as? NSSplitView else { return }

            controller = sview.delegate as? NSSplitViewController   // delegate is our controller
            if let sideBar = controller?.splitViewItems.first {     // now observe for state
                publishValue(sideBar.isCollapsed)
                observer = sideBar.observe(\.isCollapsed, options: [.new]) { [weak self] _, change in
                    if let value = change.newValue {
                        self?.publishValue(value)
                    }
                }
            }
        }

        private func publishValue(_ isCollapsed: Bool) {
            if !hasSetValue {
                hasSetValue = true
                // if collapsed at init, force-open sidebar to force list load
                if isCollapsed {
                    NSLog("open sidebar")
                    controller?.toggleSidebar(nil)
                }
            }
            sideCollapsed?.wrappedValue = isCollapsed
        }
    }
}