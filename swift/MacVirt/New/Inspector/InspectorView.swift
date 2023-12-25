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
        if let wrapper = navModel.inspectorContents {
            wrapper.contents
        } else {
            EmptyView()
        }
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
            InspectorWrapper(key: InspectorContentsKeys.def, contents: AnyView(EmptyView()))

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