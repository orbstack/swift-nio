import SwiftUI

struct DetailsLabelsSection: View {
    let labels: [String: String]

    var body: some View {
        let sortedLabels = labels.sorted { $0.key < $1.key }.map {
            KeyValueItem(key: $0.key, value: $0.value)
        }

        DetailsKvTableSection("Labels", items: sortedLabels) { item in
            Text(highlightLabel(key: item.key))
                .help(item.key)
                .truncationMode(.middle)
        } value: { item in
            CopyableText(item.value)
                .help(item.value)
        }
    }
}

private func highlightLabel(key: String) -> AttributedString {
    // make an AttributedString to highlight only the last part of the key
    var attrKey = AttributedString(key)
    if let startOfLastPart = key.lastIndex(of: ".") , let startOfLastPartAttr = AttributedString.Index(startOfLastPart, within: attrKey){
        attrKey[...startOfLastPartAttr].foregroundColor = .secondary
    }
    return attrKey
}
