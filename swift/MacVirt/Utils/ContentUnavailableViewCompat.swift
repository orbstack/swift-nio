//
// Created by Danny Lin on 7/22/23.
//

import Foundation
import SwiftUI

struct ContentUnavailableViewCompat: View {
    let title: String
    let systemImage: String?
    let desc: String?

    init(_ title: String, systemImage: String? = nil, desc: String? = nil) {
        self.title = title
        self.systemImage = systemImage
        self.desc = desc
    }

    var body: some View {
        VStack(spacing: 0) {
            if let systemImage {
                Image(systemName: systemImage)
                    .font(.system(size: 40, weight: .bold))
                    .foregroundColor(Color(NSColor.tertiaryLabelColor))
                    .padding(.bottom, 24)
            }

            Text(title)
                .font(.largeTitle.weight(.semibold))
                .foregroundColor(.secondary)

            if let desc {
                Text(desc)
                    .font(.body)
                    .foregroundColor(.secondary)
                    .padding(.top, 16)
            }
        }
    }

    static var search: some View {
        ContentUnavailableViewCompat(
            "No Results",
            systemImage: "magnifyingglass",
            desc: "Check the spelling or try a new search."
        )
    }
}