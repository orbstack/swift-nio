//
//  Utilities.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/1/23.
//

import SwiftUI

extension NSView {
    func pinEdgesToSuperview() {
        guard let superview = superview else { return }
        translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            topAnchor.constraint(equalTo: superview.topAnchor),
            rightAnchor.constraint(equalTo: superview.rightAnchor),
            bottomAnchor.constraint(equalTo: superview.bottomAnchor),
            leftAnchor.constraint(equalTo: superview.leftAnchor),
        ])
    }
}

extension NSViewController {
    func addChildViewController(_ childViewController: NSViewController, in view: NSView) {
        /// Add the view controller as a child
        addChild(childViewController)

        /// Insert as a subview
        view.addSubview(childViewController.view)

        /// Configure child view
        childViewController.view.frame = view.bounds
        childViewController.view.autoresizingMask = [.width, .height]
    }

    /// Add a child view controller inside a view with constraints.
    func embed(_ childViewController: NSViewController, in view: NSView) {
        /// Add the view controller as a child
        addChild(childViewController)

        /// Insert as a subview.
        view.addSubview(childViewController.view)

        childViewController.view.pinEdgesToSuperview()
    }

    func removeChildViewController(_ childViewController: NSViewController) {
        /// Remove child view from superview
        childViewController.view.removeFromSuperview()

        /// Notify child view controller again
        childViewController.removeFromParent()
    }
}

class WindowGrabberView: NSView {
    var movedToWindow = false
    var onMoveToWindow: ((NSWindow) -> Void)?

    override func viewWillMove(toWindow newWindow: NSWindow?) {
        if let window = newWindow {
            if movedToWindow {
                return
            } else {
                movedToWindow = true
            }

            onMoveToWindow?(window)
        }
    }
}
