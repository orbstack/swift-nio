//
// Created by Danny Lin on 7/22/23.
//

import Foundation
import SwiftUI

struct ContentUnavailableViewCompat<Actions: View>: View {
    let title: String
    let systemImage: String?
    let desc: String?
    @ViewBuilder let actions: () -> Actions

    init(
        _ title: String, systemImage: String? = nil, desc: String? = nil,
        @ViewBuilder actions: @escaping () -> Actions
    ) {
        self.title = title
        self.systemImage = systemImage
        self.desc = desc
        self.actions = actions
    }

    var body: some View {
        ContentUnavailableView {
            if let systemImage {
                Label(title, systemImage: systemImage)
            } else {
                Label {
                    Text(title)
                } icon: {
                }
            }
        } description: {
            if let desc {
                Text(desc)
            }
        } actions: {
            actions()
        }
    }
}

extension ContentUnavailableViewCompat where Actions == EmptyView {
    static var search: some View {
        ContentUnavailableViewCompat(
            "No Results",
            systemImage: "magnifyingglass",
            desc: "Check the spelling or try a new search."
        )
    }

    init(_ title: String, systemImage: String? = nil, desc: String? = nil) {
        self.init(title, systemImage: systemImage, desc: desc) { EmptyView() }
    }
}
