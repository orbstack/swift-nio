import AppKit
import Combine

/*
 *
 */

@resultBuilder
struct AKToolbarItemsBuilder {
    static func buildPartialBlock(first: NSToolbarItem) -> [NSToolbarItem] {
        [first]
    }

    static func buildPartialBlock(first: [NSToolbarItem]) -> [NSToolbarItem] {
        first
    }

    static func buildPartialBlock(accumulated: [NSToolbarItem], next: NSToolbarItem)
        -> [NSToolbarItem]
    {
        accumulated + [next]
    }

    static func buildEither<T>(first: T) -> T {
        first
    }

    static func buildEither<T>(second: T) -> T {
        second
    }

    static func buildOptional(_ item: NSToolbarItem?) -> [NSToolbarItem] {
        if let item { [item] } else { [] }
    }
}

func AKSystemToolbarItem(system identifier: NSToolbarItem.Identifier) -> NSToolbarItem {
    return NSToolbarItem(itemIdentifier: identifier)
}

final class AKToolbarItem: NSToolbarItem {
    private let closure: () -> Void
    private let cancellable: AnyCancellable? = nil

    init(
        id: String, title: String, systemIcon: String? = nil, tooltip: String? = nil,
        action: @escaping () -> Void
    ) {
        self.closure = action

        super.init(itemIdentifier: NSToolbarItem.Identifier(id))
        self.target = self
        self.action = #selector(performAction)
        self.label = title
        self.toolTip = tooltip ?? title
        if let systemIcon {
            self.image = NSImage(systemSymbolName: systemIcon, accessibilityDescription: nil)!
        }
    }

    @objc private func performAction() {
        closure()
    }
}
