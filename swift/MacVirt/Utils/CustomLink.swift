//
// Created by Danny Lin on 8/28/23.
//

import Foundation
import SwiftUI

struct CustomLink: View {
    let text: String
    let onClick: () -> Void

    init(_ text: String, onClick: @escaping () -> Void) {
        self.text = text
        self.onClick = onClick
    }

    init(_ text: String, url: URL) {
        self.text = text
        self.onClick = {
            NSWorkspace.shared.open(url)
        }
    }

    var body: some View {
        Text(text)
        .foregroundColor(.blue)
        .onHover { inside in
            if inside {
                NSCursor.pointingHand.push()
            } else {
                NSCursor.pop()
            }
        }
        .onTapGesture {
            onClick()
        }
    }
}