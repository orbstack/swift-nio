//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct DetailsStack<Content: View>: View {
    @ViewBuilder private let content: () -> Content

    init(@ViewBuilder content: @escaping () -> Content) {
        self.content = content
    }

    var body: some View {
        Form {
            content()
        }
        .formStyle(.grouped)
    }
}

struct DetailsKvSection<Content: View>: View {
    private let label: String?
    @ViewBuilder private let content: () -> Content

    init(_ label: String? = nil, @ViewBuilder content: @escaping () -> Content) {
        self.label = label
        self.content = content
    }

    var body: some View {
        Section {
            content()
        } header: {
            if let label {
                Text(label)
            }
        }
    }
}

struct DetailsListSection<Content: View>: View {
    private let label: String
    @ViewBuilder private let content: () -> Content

    init(_ label: String, @ViewBuilder content: @escaping () -> Content) {
        self.label = label
        self.content = content
    }

    var body: some View {
        Section {
            content()
        } header: {
            Text(label)
        }
    }
}

struct DetailsRow<Content: View>: View {
    private let label: String
    @ViewBuilder private let content: () -> Content

    init(_ label: String, @ViewBuilder content: @escaping () -> Content) {
        self.label = label
        self.content = content
    }

    var body: some View {
        LabeledContent {
            content()
        } label: {
            Text(label)
        }
    }
}

extension DetailsRow where Content == Text {
    init(_ label: String, text: String) {
        self.label = label
        self.content = {
            Text(text)
        }
    }
}

extension DetailsRow where Content == CopyableText<Text> {
    init(_ label: String, copyableText: String, copyAs: String? = nil) {
        self.label = label
        self.content = {
            CopyableText(copyableText, copyAs: copyAs)
        }
    }
}

struct DetailsTableSection<Content: View>: View {
    private let label: String
    @ViewBuilder private let content: () -> Content

    init(_ label: String, @ViewBuilder content: @escaping () -> Content) {
        self.label = label
        self.content = content
    }

    var body: some View {
        Section {
            content()
        } header: {
            Text(label)
        }
    }
}

struct KeyValueItem: Identifiable {
    let key: String
    let value: String

    var id: String { key }
}

struct DetailsKvTableSection<Key: View, Value: View>: View {
    private let label: String
    private let items: [KeyValueItem]
    @ViewBuilder private let key: (KeyValueItem) -> Key
    @ViewBuilder private let value: (KeyValueItem) -> Value

    init(_ label: String, items: [KeyValueItem], @ViewBuilder key: @escaping (KeyValueItem) -> Key = { Text($0.key) }, @ViewBuilder value: @escaping (KeyValueItem) -> Value = { CopyableText($0.value) }) {
        self.label = label
        self.items = items
        self.key = key
        self.value = value
    }

    var body: some View {
        Section {
            Table(items) {
                TableColumn("Key") { item in
                    key(item)
                }
                TableColumn("Value") { item in
                    value(item)
                }
            }
        } header: {
            Text(label)
        }
    }
}
