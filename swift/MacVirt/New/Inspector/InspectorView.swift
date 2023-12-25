//
//  InspectorView.swift
//  MacVirt
//
//  Created by Andrew Zheng on 12/1/23.
//

import SwiftUI

class InspectorViewController: NSViewController {
    init() {
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder _: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    override func loadView() {
        let contentView = InspectorView()
        let hostingView = NSHostingView(rootView: contentView)
        view = hostingView
    }
}

struct InspectorView: View {
    var body: some View {
        ScrollView {
            VStack {
                ForEach(0 ..< 30) { _ in
                    RoundedRectangle(cornerRadius: 16)
                        .fill(.teal)
                        .frame(height: 50)
                }
            }
            .padding(.horizontal)
            .padding(.vertical)
        }
    }
}
