//
// Created by Danny Lin on 9/4/23.
//

import AppKit
import SwiftUI
import Combine

private class AKOutlineView: NSOutlineView {
    // workaround for off-center disclosure arrow: https://stackoverflow.com/a/74894605
    override func frameOfOutlineCell(atRow row: Int) -> NSRect {
        super.frameOfOutlineCell(atRow: row)
    }

    // we get here if the right click wasn't handled by SwiftUI, usually b/c out of bounds
    // e.g. clicked on arrow or margin
    // never use the fake menu, but try to forward it
    override func menu(for event: NSEvent) -> NSMenu? {
        // call super so that it sets clickedRow. (highlight ring won't trigger until willOpenMenu)
        super.menu(for: event)

        // find the clicked view
        if clickedRow != -1,
           let view = self.view(atColumn: 0, row: clickedRow, makeIfNecessary: false) {
            // make a fake event for its center
            let center = CGPointMake(NSMidX(view.frame), NSMidY(view.frame))
            // ... relative to the window
            let centerInWindow = view.convert(center, to: nil)

            if let fakeEvent = NSEvent.mouseEvent(
                with: event.type,
                location: centerInWindow,
                modifierFlags: event.modifierFlags,
                timestamp: event.timestamp,
                windowNumber: event.windowNumber,
                context: nil, // deprecated
                eventNumber: event.eventNumber,
                clickCount: event.clickCount,
                pressure: event.pressure
            ) {
                return view.menu(for: fakeEvent)
            }
        }

        // failed to forward
        return nil
    }

    // for AKHostingView to trigger highlight
    func injectMenu(for event: NSEvent) {
        super.menu(for: event)
    }
}

// forward menu request/open/close events to NSOutlineView so it triggers highlight ring,
// but *actually* use the menu from SwiftUI
private class AKHostingView<V: View>: NSHostingView<V> {
    weak var outlineParent: AKOutlineView?

    override func menu(for event: NSEvent) -> NSMenu? {
        // trigger NSOutlineView's highlight
        outlineParent?.injectMenu(for: event)
        return super.menu(for: event)
    }

    // forward menu events
    override func willOpenMenu(_ menu: NSMenu, with event: NSEvent) {
        super.willOpenMenu(menu, with: event)
        outlineParent?.willOpenMenu(menu, with: event)
    }

    override func didCloseMenu(_ menu: NSMenu, with event: NSEvent?) {
        super.didCloseMenu(menu, with: event)
        outlineParent?.didCloseMenu(menu, with: event)
    }

    override func mouseDown(with event: NSEvent) {
        super.mouseDown(with: event)
    }
}

private class AKListModel: ObservableObject {
    let doubleClicks = PassthroughSubject<AnyHashable, Never>()
}

private class AKListItemModel: ObservableObject {
    let itemId: AnyHashable

    init(itemId: AnyHashable) {
        self.itemId = itemId
    }
}

typealias AKListItemBase = Identifiable & Equatable

// AKListItem is for both hierarchical and flat lists, with a default impl of listChildren.
// it requires protocol conformance, but nothing more
// allows unifying AKTreeList and AKFlatList impl
// and faster for the flat case (no need for wrapper objects)
protocol AKListItem: AKListItemBase {
    var listChildren: [any AKListItem]? { get }
}

extension AKListItem {
    var listChildren: [any AKListItem]? {
        nil
    }
}

private struct FlatItemWrapper<V: AKListItemBase>: AKListItem {
    let value: V

    var id: V.ID {
        value.id
    }

    var listChildren: [any AKListItem]? {
        nil
    }
}

struct AKSection<Element: AKListItemBase>: AKListItemBase {
    // nil = no header
    let title: String?
    let items: [Element]

    var id: String? {
        title
    }

    static func single(_ items: [Element]) -> [AKSection<Element>] {
        [AKSection(title: nil, items: items)]
    }
}

@objc protocol AKNode {}

private class AKItemNode: NSObject, AKNode {
    @objc dynamic var children: [AKItemNode]?
    @objc dynamic var isLeaf: Bool = false
    @objc dynamic var count: Int {
        children?.count ?? 0
    }

    var value: any AKListItem

    init(value: any AKListItem) {
        self.value = value
    }
}

private class AKSectionNode: NSObject, AKNode {
    @objc dynamic var children: [AKSectionNode]? { nil }
    @objc dynamic var isLeaf: Bool { true }
    @objc dynamic var count: Int { 0 }

    var value: String

    init(value: String) {
        self.value = value
    }
}

private struct AKTreeListImpl<Item: AKListItem, ItemView: View>: NSViewRepresentable, Equatable {
    @StateObject private var envModel = AKListModel()

    let sections: [AKSection<Item>]
    @Binding var selection: Set<Item.ID>
    let rowHeight: CGFloat?
    let singleSelection: Bool
    let makeRowView: (Item) -> ItemView

    static func == (lhs: AKTreeListImpl, rhs: AKTreeListImpl) -> Bool {
        // row callback should never change
        lhs.sections == rhs.sections &&
            lhs.selection == rhs.selection &&
            lhs.rowHeight == rhs.rowHeight
    }

    final class Coordinator: NSObject, NSOutlineViewDelegate {
        var parent: AKTreeListImpl

        @objc fileprivate dynamic var content: [AKNode] = []
        var lastSections: [AKSection<Item>] = []

        var treeController: NSTreeController!
        private var observation: NSKeyValueObservation?

        // preserve objc object identity to avoid losing state
        private var objCache = [Item.ID: AKItemNode]()
        // array is fastest since we just iterate and clear this
        private var objAccessTracker = [Item.ID]()

        init(_ parent: AKTreeListImpl) {
            self.parent = parent
        }

        // make views
        func outlineView(_ outlineView: NSOutlineView, viewFor tableColumn: NSTableColumn?, item: Any) -> NSView? {
            let nsNode = item as! NSTreeNode

            // TODO use outlineView.makeView to reuse views
            if let node = nsNode.representedObject as? AKItemNode {
                let itemModel = AKListItemModel(itemId: node.value.id as! AnyHashable)
                let view = parent.makeRowView(node.value as! Item)
                .environmentObject(parent.envModel)
                .environmentObject(itemModel)

                // menu forwarder
                let nsView = AKHostingView(rootView: view)
                nsView.outlineParent = (outlineView as! AKOutlineView)
                return nsView
            } else if let node = nsNode.representedObject as? AKSectionNode {
                let view = NSTextField(labelWithString: node.value)
                view.font = NSFont.boldSystemFont(ofSize: NSFont.systemFontSize)
                return view
            } else {
                return nil
            }
        }

        func outlineView(_ outlineView: NSOutlineView, heightOfRowByItem item: Any) -> CGFloat {
            parent.rowHeight ?? 0
        }

        @objc func onDoubleClick(_ sender: Any) {
            // expand or collapse row
            let outlineView = sender as! NSOutlineView
            let row = outlineView.clickedRow
            guard row != -1 else {
                return
            }

            let item = outlineView.item(atRow: row)
            if outlineView.isItemExpanded(item) {
                outlineView.animator().collapseItem(item)
            } else {
                outlineView.animator().expandItem(item)
            }

            // emit double click event via notification center
            let nsNode = item as! NSTreeNode
            if let node = nsNode.representedObject as? AKItemNode {
                parent.envModel.doubleClicks.send(node.value.id as! AnyHashable)
            }
        }

        func mapNode(item: Item) -> AKItemNode {
            var node: AKItemNode
            if let cachedNode = objCache[item.id] {
                node = cachedNode
            } else {
                node = AKItemNode(value: item)
                objCache[item.id] = node
            }
            objAccessTracker.append(item.id)

            // update the node
            let listChildren = item.listChildren
            node.isLeaf = listChildren == nil || listChildren!.isEmpty
            node.children = listChildren?.map { mapNode(item: $0 as! Item) }
            node.value = item
            return node
        }

        func mapAllNodes(sections: [AKSection<Item>]) -> [AKNode] {
            // record accessed nodes
            let newNodes = sections.flatMap {
                // more efficient than map and concat
                var sectionNodes = [AKNode]()
                sectionNodes.reserveCapacity($0.items.count + 1)
                if let title = $0.title {
                    sectionNodes.append(AKSectionNode(value: title))
                }
                for item in $0.items {
                    sectionNodes.append(mapNode(item: item))
                }
                return sectionNodes
            }

            // remove unused nodes
            let unusedNodes = objCache.filter { !objAccessTracker.contains($0.key) }
            for (id, _) in unusedNodes {
                objCache.removeValue(forKey: id)
            }

            // clear access tracker
            objAccessTracker.removeAll()
            return newNodes
        }

        func outlineViewSelectionDidChange(_ notification: Notification) {
            let selectedIds = treeController.selectedObjects
                .compactMap { ($0 as? AKItemNode)?.value.id as? Item.ID }
            DispatchQueue.main.async {
                print("et selection to: \(selectedIds)")
                self.parent.selection = Set(selectedIds)
                print("read back = \(self.parent.selection)")
            }
        }

        func outlineView(_ outlineView: NSOutlineView, isGroupItem item: Any) -> Bool {
            let nsNode = item as! NSTreeNode
            return nsNode.representedObject is AKSectionNode
        }

//        func setTreeController(_ treeController: NSTreeController) {
//            self.treeController = treeController
//
//            // observe KVO
//            observation = treeController.observe(\.selectedObjects, options: [.new]) { [weak self] _, change in
//                guard let self = self else { return }
//                let selectedIds = treeController.selectedObjects
//                    .compactMap { ($0 as? AKItemNode)?.value.id as? Item.ID }
//                print("set selection to: \(selectedIds)")
//                parent.selection = Set(selectedIds)
//            }
//        }
    }

    func makeNSView(context: Context) -> NSScrollView {
        let coordinator = context.coordinator
        coordinator.parent = self

        let treeController = NSTreeController()
        treeController.bind(.contentArray, to: coordinator, withKeyPath: "content")
        treeController.objectClass = AKItemNode.self
        treeController.childrenKeyPath = "children"
        treeController.countKeyPath = "count"
        treeController.leafKeyPath = "isLeaf"
        treeController.preservesSelection = true
        treeController.avoidsEmptySelection = false
        treeController.selectsInsertedObjects = false
        treeController.alwaysUsesMultipleValuesMarker = true // perf
//        coordinator.setTreeController(treeController)
        coordinator.treeController = treeController

        let outlineView = AKOutlineView()
        outlineView.delegate = coordinator
        outlineView.bind(.content, to: treeController, withKeyPath: "arrangedObjects")
        outlineView.bind(.selectionIndexPaths, to: treeController, withKeyPath: "selectionIndexPaths")
        // fix width changing when expanding/collapsing
        outlineView.autoresizesOutlineColumn = false
        outlineView.allowsMultipleSelection = !singleSelection
        outlineView.allowsEmptySelection = true
        outlineView.usesAutomaticRowHeights = rowHeight == nil
        // dummy menu to trigger highlight
        outlineView.menu = NSMenu()

        // hide header
        outlineView.headerView = nil

        // use outlineView's double click. more reliable than Swift onDoubleClick
        outlineView.target = coordinator
        outlineView.doubleAction = #selector(Coordinator.onDoubleClick)

        // add one column
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("column"))
        column.isEditable = false
        outlineView.addTableColumn(column)

        let scrollView = NSScrollView()
        scrollView.documentView = outlineView
        scrollView.hasVerticalScroller = true
        return scrollView
    }

    func updateNSView(_ nsView: NSScrollView, context: Context) {
        let coordinator = context.coordinator
        coordinator.parent = self
        guard sections != coordinator.lastSections else {
            return
        }
        print("update nsview: \(coordinator)")

        // convert to nodes
        let nodes = coordinator.mapAllNodes(sections: sections)
        // update tree controller and reload view (via KVO)
        coordinator.content = nodes
        coordinator.lastSections = sections
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(self)
    }
}

// Hierarchical list using AppKit's NSOutlineView and NSTreeController.
// Can also be used for non-hierarchical lists.
//
// TO MIGRATE FROM SwiftUI List:
//   - List -> AKList
//   - Section -> [AKSection]
//   - .onRawDoubleClick -> .akListOnDoubleClick
//   - .contextMenu -> .akListContextMenu
//   - increase .vertical padding (4->8) to match SwiftUI List
//   - add .environmentObjects to the item view
//   - (optional) set rowHeight: for performance
//
// benefits:
//   - fix black bar w/o covering up rect
//     -> fixes scrollbar
//   - slightly faster
//   - double click to expand
//   - no random holes / buggy behavior
//   - row separator lines, but only when selected
//   - should no longer crash
//   - scroll position moves to follow selection
//   - native double click implementation for reliability
//   - sections for hierarchical lists
struct AKList<Item: AKListItem, ItemView: View>: View {
    private let sections: [AKSection<Item>]
    @Binding private var selection: Set<Item.ID>
    private let rowHeight: CGFloat?
    private let makeRowView: (Item) -> ItemView
    private let singleSelection: Bool

    // hierarchical OR flat, with sections, multiple selection
    init(_ sections: [AKSection<Item>],
         selection: Binding<Set<Item.ID>>,
         rowHeight: CGFloat? = nil,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.sections = sections
        self._selection = selection
        self.rowHeight = rowHeight
        self.makeRowView = makeRowView
        self.singleSelection = false
    }

    // hierarchical OR flat, with sections, single selection
    init(_ sections: [AKSection<Item>],
         selection singleBinding: Binding<Item.ID?>,
         rowHeight: CGFloat? = nil,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.sections = sections
        self._selection = Binding<Set<Item.ID>>(
            get: {
                if let id = singleBinding.wrappedValue {
                    return [id]
                } else {
                    return []
                }
            },
            set: {
                singleBinding.wrappedValue = $0.first
            })
        self.rowHeight = rowHeight
        self.makeRowView = makeRowView
        self.singleSelection = true
    }

    var body: some View {
        AKTreeListImpl(sections: sections,
                selection: $selection,
                rowHeight: rowHeight,
                singleSelection: singleSelection,
                makeRowView: makeRowView)
        // TODO: is this useless?
        //.equatable()
        // fix toolbar color and blur (fullSizeContentView)
        .ignoresSafeArea()
    }
}

// structs can't have convenience init, so use an extension
extension AKList {
    // hierarchical OR flat, no sections, multiple selection
    init(_ items: [Item],
         selection: Binding<Set<Item.ID>>,
         rowHeight: CGFloat? = nil,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.init(AKSection.single(items),
                selection: selection,
                rowHeight: rowHeight,
                makeRowView: makeRowView)
    }

    // hierarchical OR flat, no sections, single selection
    init(_ items: [Item],
         selection singleBinding: Binding<Item.ID?>,
         rowHeight: CGFloat? = nil,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.init(AKSection.single(items),
                selection: singleBinding,
                rowHeight: rowHeight,
                makeRowView: makeRowView)
    }
}

private struct DoubleClickViewModifier: ViewModifier {
    @EnvironmentObject private var listModel: AKListModel
    @EnvironmentObject private var itemModel: AKListItemModel

    let action: () -> Void

    func body(content: Content) -> some View {
        content
            .onReceive(listModel.doubleClicks) { id in
                if id == itemModel.itemId {
                    action()
                }
            }
    }
}

extension View {
    // SwiftUI rejects menu(forEvent:) unless it thinks it owns the view at which
    // the click occurred. onDoubleClick makes a big NSView to fix this
    func akListContextMenu<MenuItems: View>(@ViewBuilder menuItems: () -> MenuItems) -> some View {
        self
            .onRawDoubleClick { }
            .contextMenu {
                menuItems()
            }
    }

    func akListOnDoubleClick(perform action: @escaping () -> Void) -> some View {
        self.modifier(DoubleClickViewModifier(action: action))
    }
}