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
    @EnvironmentObject var model: VmViewModel
    @EnvironmentObject var navModel: MainNavViewModel

    var body: some View {
        VStack {
            if let wrapper = navModel.inspectorContents {
                wrapper.contents
            } else {
                EmptyView()
            }
        }
        // you don't need this when you have a scroll view,
        // but make sure to expand to fill all space.
        // otherwise, the split view layout will break.
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

struct InspectorWrapper: Equatable {
    let key: AnyHashable
    let contents: AnyView

    static func == (lhs: InspectorWrapper, rhs: InspectorWrapper) -> Bool {
        lhs.key == rhs.key
    }
}

private enum InspectorContentsKeys {
    case def
}

struct InspectorContentsKey: PreferenceKey {
    static var defaultValue: InspectorWrapper =
        .init(key: InspectorContentsKeys.def, contents: AnyView(EmptyView()))

    static func reduce(value: inout InspectorWrapper, nextValue: () -> InspectorWrapper) {
        let nextVal = nextValue()
        if nextVal.key != AnyHashable(InspectorContentsKeys.def) {
            value = nextVal
        }
    }
}

extension View {
    func inspectorContents<Key: Hashable>(key: Key, listModel: AKListModel, _ contents: @escaping () -> some View) -> some View {
        self.if(listModel.selection.contains(AnyHashable(key))) { view in
            view.preference(key: InspectorContentsKey.self,
                            value: InspectorWrapper(key: AnyHashable(key), contents: AnyView(contents())))
        }
    }
}
