//
//  MenuItems.swift
//  MacVirt
//
//  Created by emmie on 11/19/24.
//
import AppKit
import SwiftUI

private class ActionItemController: NSObject {
    private let action: () -> Void

    init(action: @escaping () -> Void) {
        self.action = action
        super.init()
    }

    @objc func action(_: NSMenuItem) {
        action()
    }
}

public struct RIMenuItem {
    public let item: NSMenuItem

    public init(_ item: NSMenuItem) {
        self.item = item
    }

    public init(_ title: String) {
        self.item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
    }

    public init(_ title: NSAttributedString) {
        self.init("")
        self.item.attributedTitle = title
    }

    public init(
        _ title: String, action: @escaping () -> Void
    ) {
        self.init(title)
        let controller = ActionItemController(action: action)
        self.item.action = #selector(controller.action)
        self.item.target = controller
        // retain
        self.item.representedObject = controller
    }

    public init(
        _ title: NSAttributedString, action: @escaping () -> Void
    ) {
        self.init("")
        self.item.attributedTitle = title
    }

    public init(
        _ title: String, asyncAction: @escaping () async -> Void
    ) {
        self.init(title) {
            Task { @MainActor in
                await asyncAction()
            }
        }
    }

    public init(
        _ title: NSAttributedString, asyncAction: @escaping () async -> Void
    ) {
        self.init("", asyncAction: asyncAction)
        self.item.attributedTitle = title
    }

    public init(submenu: RIMenu) {
        self.init(
            NSMenuItem(title: submenu.menu.title, action: nil, keyEquivalent: submenu.shortcut))
        self.item.submenu = submenu.menu
    }

    public func disabled(_ disabled: Bool) -> RIMenuItem {
        self.item.isEnabled = !disabled
        return self
    }

    public func icon(_ icon: NSImage?) -> RIMenuItem {
        self.item.image = icon
        return self
    }

    public func shortcut(_ shortcut: String) -> RIMenuItem {
        self.item.keyEquivalent = shortcut
        return self
    }

    public func state(_ state: NSControl.StateValue) -> RIMenuItem {
        self.item.state = state
        return self
    }

    public static func separator() -> RIMenuItem {
        RIMenuItem(NSMenuItem.separator())
    }

    public static func sectionHeader(_ contents: String) -> RIMenuItem {
        RIMenuItem(
            NSAttributedString(
                string: contents,
                attributes: [
                    NSAttributedString.Key.font: NSFont.systemFont(ofSize: 12, weight: .bold),
                    NSAttributedString.Key.foregroundColor: NSColor.labelColor,
                ]))
    }

    public static func infoLine(_ contents: String) -> RIMenuItem {
        RIMenuItem(contents)
    }

    public static func truncatedItems<T>(
        _ items: [T],
        overflowHeader: String? = nil,
        overflowItems: [T]? = nil,
        maxQuickItems: Int = 3,
        makeItem: (T) -> RIMenuItem?
    ) -> [RIMenuItem] {
        var menuItems = items.prefix(maxQuickItems).compactMap(makeItem)
        if let overflowHeader {
            menuItems.append(RIMenuItem(overflowHeader))
        }
        if let overflowItems {
            menuItems.append(
                RIMenuItem(
                    submenu: RIMenu {
                        overflowItems.compactMap(makeItem)
                    }))
        }
        return menuItems
    }
}

public enum RIMenuElement {
    case item(RIMenuItem)
    case menu(RIMenu)
    case elements(RIMenuElements)
}

public struct RIMenuPlacementConstraint {
    public let position: Int
    public let relativeTo: NSMenuItem?

    public init(position: Int, relativeTo: NSMenuItem? = nil) {
        self.position = position
        self.relativeTo = relativeTo
    }
}

public struct RIMenuElements {
    public let elements: [RIMenuElement]
    public let placementConstraint: RIMenuPlacementConstraint?

    public init(_ elements: [RIMenuElement], placementConstraint: RIMenuPlacementConstraint? = nil)
    {
        self.elements = elements
        self.placementConstraint = placementConstraint
    }

    public init(
        placementConstraint: RIMenuPlacementConstraint? = nil,
        @RIMenuElementsResultBuilder _ builder: () -> RIMenuElements
    ) {
        self.elements = builder().elements
        self.placementConstraint = placementConstraint
    }

    public func insertMenuItems(to: NSMenu, at: Int) -> (Int, [NSMenuItem]) {
        var index = at
        var items: [NSMenuItem] = []
        elements.forEach { element in
            switch element {
            case .item(let item):
                to.insertItem(item.item, at: index)
                items.append(item.item)
                index += 1
            case .menu(let menu):
                let item = RIMenuItem(submenu: menu).item
                to.insertItem(item, at: index)
                items.append(item)
                index += 1
            case .elements(let elements):
                if elements.placementConstraint != nil {
                    let addedItems = elements.addMenuItems(to: to)
                    items.append(contentsOf: addedItems)
                } else {
                    let (newIndex, addedItems) = elements.insertMenuItems(to: to, at: index)
                    items.append(contentsOf: addedItems)
                    index = newIndex
                }
            }
        }
        return (index, items)
    }

    public func insertMenuItems(to: RIMenu, at: Int) -> (Int, [NSMenuItem]) {
        insertMenuItems(to: to.menu, at: at)
    }

    public func addMenuItems(to: NSMenu) -> [NSMenuItem] {
        if let placementConstraint = placementConstraint {
            let placementBaseIndex = placementConstraint.relativeTo.map { to.index(of: $0) } ?? 0
            let (_, items) = insertMenuItems(
                to: to, at: placementBaseIndex + placementConstraint.position)
            return items
        } else {
            var items: [NSMenuItem] = []
            elements.forEach { element in
                switch element {
                case .item(let item):
                    to.addItem(item.item)
                    items.append(item.item)
                case .menu(let menu):
                    let item = RIMenuItem(submenu: menu).item
                    to.addItem(item)
                    items.append(item)
                case .elements(let elements):
                    let addedItems = elements.addMenuItems(to: to)
                    items.append(contentsOf: addedItems)
                }
            }
            return items
        }
    }

    public func addMenuItems(to menu: RIMenu) -> [NSMenuItem] {
        self.addMenuItems(to: menu.menu)
    }

    public func setMenuItems(to menu: NSMenu) -> [NSMenuItem] {
        menu.removeAllItems()
        return self.addMenuItems(to: menu)
    }

    public func setMenuItems(to menu: RIMenu) -> [NSMenuItem] {
        self.setMenuItems(to: menu.menu)
    }
}

public struct RIMenu {
    public let menu: NSMenu

    // for use as a submenu
    public let shortcut: String

    public init(_ menu: NSMenu, shortcut: String = "") {
        self.menu = menu
        self.shortcut = ""
    }

    public init(
        _ title: String = "", shortcut: String = "",
        @RIMenuElementsResultBuilder _ builder: () -> RIMenuElements
    ) {
        self.init(NSMenu(title: title))
        let _ = builder().setMenuItems(to: self)
    }
}

@resultBuilder
public struct RIMenuElementsResultBuilder {
    public static func buildFinalResult(_ component: [RIMenuElement]) -> RIMenuElements {
        RIMenuElements(component)
    }

    public static func buildBlock(_ components: [RIMenuElement]...) -> [RIMenuElement] {
        Array(components.joined())
    }

    public static func buildOptional(_ component: [RIMenuElement]?) -> [RIMenuElement] {
        component ?? []
    }

    public static func buildEither(first component: [RIMenuElement]) -> [RIMenuElement] {
        component
    }

    public static func buildEither(second component: [RIMenuElement]) -> [RIMenuElement] {
        component
    }

    public static func buildArray(_ components: [[RIMenuElement]]) -> [RIMenuElement] {
        Array(components.joined())
    }

    public static func buildExpression(_ expression: RIMenuElement) -> [RIMenuElement] {
        [expression]
    }

    public static func buildExpression(_ expression: [RIMenuElement]) -> [RIMenuElement] {
        expression
    }

    public static func buildExpression(_ expression: RIMenuItem) -> [RIMenuElement] {
        [.item(expression)]
    }

    public static func buildExpression(_ expression: RIMenuItem?) -> [RIMenuElement] {
        expression.map { [.item($0)] } ?? []
    }

    public static func buildExpression(_ expression: [RIMenuItem]) -> [RIMenuElement] {
        expression.map { .item($0) }
    }

    public static func buildExpression(_ expression: RIMenu) -> [RIMenuElement] {
        [.menu(expression)]
    }

    public static func buildExpression(_ expression: [RIMenu]) -> [RIMenuElement] {
        expression.map { .menu($0) }
    }

    public static func buildExpression(_ expression: RIMenuElements) -> [RIMenuElement] {
        [.elements(expression)]
    }

    public static func buildExpression(_ expression: [RIMenuElements]) -> [RIMenuElement] {
        expression.map { .elements($0) }
    }

    public static func buildExpression(_ expression: NSMenuItem) -> [RIMenuElement] {
        [.item(RIMenuItem(expression))]
    }

    public static func buildExpression(_ expression: NSMenu) -> [RIMenuElement] {
        [.menu(RIMenu(expression))]
    }
}

extension NSMenu {
    public func riAddMenuItems(@RIMenuElementsResultBuilder _ builder: () -> RIMenuElements)
        -> [NSMenuItem]
    {
        builder().addMenuItems(to: self)
    }

    public func riSetMenuItems(@RIMenuElementsResultBuilder _ builder: () -> RIMenuElements)
        -> [NSMenuItem]
    {
        builder().setMenuItems(to: self)
    }
}

public struct RIMenuManager {
    public var items: [NSMenu: [NSMenuItem]]

    public init() {
        self.items = [:]
    }

    public mutating func updateMenu(_ menu: NSMenu, _ elements: RIMenuElements) {
        for item in items[menu] ?? [] {
            menu.removeItem(item)
        }
        items[menu] = elements.addMenuItems(to: menu)
    }

    public mutating func updateMenu(
        _ menu: NSMenu, @RIMenuElementsResultBuilder _ builder: () -> RIMenuElements
    ) {
        self.updateMenu(menu, builder())
    }

    public mutating func updateMenu(
        _ menu: NSMenu?, @RIMenuElementsResultBuilder _ builder: () -> RIMenuElements
    ) {
        guard let menu = menu else { return }
        self.updateMenu(menu, builder())
    }
}
