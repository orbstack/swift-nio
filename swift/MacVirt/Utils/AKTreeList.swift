//
// Created by Danny Lin on 9/4/23.
//

import AppKit
import SwiftUI

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
        if clickedRow >= 0,
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

protocol AKTreeListItem: Identifiable, Equatable {
    var listChildren: [any AKTreeListItem]? { get }
}

typealias AKFlatListItem = Identifiable & Equatable

private struct FlatItemWrapper<V: AKFlatListItem>: AKTreeListItem {
    let value: V

    var id: V.ID {
        value.id
    }

    var listChildren: [any AKTreeListItem]? {
        nil
    }
}

private class AKTreeNode: NSObject {
    @objc dynamic var children: [AKTreeNode]?
    @objc dynamic var isLeaf: Bool = false
    @objc dynamic var childCount: Int {
        children?.count ?? 0
    }

    var value: any AKTreeListItem

    init(value: any AKTreeListItem) {
        self.value = value
    }
}

private struct AKTreeListImpl<Item: AKTreeListItem, ItemView: View>: NSViewRepresentable, Equatable {
    let items: [Item]
    @Binding var selection: Set<Item.ID>
    let rowHeight: CGFloat
    let singleSelection: Bool
    let makeRowView: (Item) -> ItemView

    static func == (lhs: AKTreeListImpl, rhs: AKTreeListImpl) -> Bool {
        // row callback should never change
        lhs.items == rhs.items &&
            lhs.selection == rhs.selection &&
            lhs.rowHeight == rhs.rowHeight
    }

    final class Coordinator: NSObject, NSOutlineViewDelegate {
        var parent: AKTreeListImpl

        @objc fileprivate dynamic var content: [AKTreeNode] = []
        var lastItems: [Item] = []

        var treeController: NSTreeController!
        private var observation: NSKeyValueObservation?

        // preserve objc object identity to avoid losing state
        private var objCache = [Item.ID: AKTreeNode]()
        // array is fastest since we just iterate and clear this
        private var objAccessTracker = [Item.ID]()

        init(_ parent: AKTreeListImpl) {
            self.parent = parent
        }

        // make views
        func outlineView(_ outlineView: NSOutlineView, viewFor tableColumn: NSTableColumn?, item: Any) -> NSView? {
            guard let nsNode = item as? NSTreeNode,
                  let node = nsNode.representedObject as? AKTreeNode else {
                return nil
            }

            let view = parent.makeRowView(node.value as! Item)
            // menu forwarder
            let nsView = AKHostingView(rootView: view)
            nsView.outlineParent = outlineView as! AKOutlineView
            return nsView
        }

        func outlineView(_ outlineView: NSOutlineView, heightOfRowByItem item: Any) -> CGFloat {
            parent.rowHeight
        }

        @objc func onDoubleClick(_ sender: Any) {
            // expand or collapse row
            if let outlineView = sender as? NSOutlineView {
                let row = outlineView.clickedRow
                if row != -1 {
                    let item = outlineView.item(atRow: row)
                    if outlineView.isItemExpanded(item) {
                        outlineView.animator().collapseItem(item)
                    } else {
                        outlineView.animator().expandItem(item)
                    }
                }
            }

            // TODO emit event via notification center
        }

        func mapNode(item: Item) -> AKTreeNode {
            var node: AKTreeNode
            if let cachedNode = objCache[item.id] {
                node = cachedNode
            } else {
                node = AKTreeNode(value: item)
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

        func mapAllNodes(items: [Item]) -> [AKTreeNode] {
            // record accessed nodes
            let newNodes = items.map { mapNode(item: $0) }

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
                .compactMap { ($0 as? AKTreeNode)?.value.id as? Item.ID }
            DispatchQueue.main.async {
                print("et selection to: \(selectedIds)")
                self.parent.selection = Set(selectedIds)
                print("read back = \(self.parent.selection)")
            }
        }

//        func setTreeController(_ treeController: NSTreeController) {
//            self.treeController = treeController
//
//            // observe KVO
//            observation = treeController.observe(\.selectedObjects, options: [.new]) { [weak self] _, change in
//                guard let self = self else { return }
//                let selectedIds = treeController.selectedObjects
//                    .compactMap { ($0 as? AKTreeNode)?.value.id as? Item.ID }
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
        treeController.objectClass = AKTreeNode.self
        treeController.childrenKeyPath = "children"
        treeController.countKeyPath = "childCount"
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
        guard items != coordinator.lastItems else {
            return
        }
        print("update nsview: \(coordinator)")

        // convert to nodes
        let nodes = coordinator.mapAllNodes(items: items)
        // update tree controller and reload view (via KVO)
        coordinator.content = nodes
        coordinator.lastItems = items
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(self)
    }
}

// Hierarchial list using AppKit's NSOutlineView and NSTreeController.
// Can also be used for non-hierarchial lists - one impl works.
//
// TO MIGRATE FROM SwiftUI List:
//   - List -> AKList
//   - calculate a rowHeight
//   - .onRawDoubleClick -> .akListOnDoubleClick
//   - .contextMenu -> .akListContextMenu
//   - increase .vertical padding (4->8) to match SwiftUI List
//   - add .environmentObjects to the item view
struct AKTreeList<Item: AKTreeListItem, ItemView: View>: View {
    private let items: [Item]
    @Binding var selection: Set<Item.ID>
    private let rowHeight: CGFloat
    private let makeRowView: (Item) -> ItemView

    init(_ items: [Item],
         selection: Binding<Set<Item.ID>>,
         rowHeight: CGFloat,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.items = items
        self._selection = selection
        self.rowHeight = rowHeight
        self.makeRowView = makeRowView
    }

    var body: some View {
        AKTreeListImpl(items: items,
                selection: $selection,
                rowHeight: rowHeight,
                singleSelection: false,
                makeRowView: makeRowView)
        // TODO: is this useless?
        //.equatable()
        // fix toolbar color and blur (fullSizeContentView)
        .ignoresSafeArea()
    }
}

struct AKFlatList<Item: AKFlatListItem, ItemView: View>: View {
    private let items: [FlatItemWrapper<Item>]
    @Binding var selection: Set<Item.ID>
    private let rowHeight: CGFloat
    private let makeRowView: (Item) -> ItemView
    private let singleSelection: Bool

    init(_ items: [Item],
         selection: Binding<Set<Item.ID>>,
         rowHeight: CGFloat,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.items = items.map { FlatItemWrapper(value: $0) }
        self._selection = selection
        self.rowHeight = rowHeight
        self.makeRowView = makeRowView
        self.singleSelection = false
    }

    init(_ items: [Item],
         selection singleBinding: Binding<Item.ID?>,
         rowHeight: CGFloat,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.items = items.map { FlatItemWrapper(value: $0) }
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
        AKTreeListImpl(items: items,
                selection: $selection,
                rowHeight: rowHeight,
                singleSelection: singleSelection) {
            makeRowView($0.value)
        }
        // fix toolbar color and blur (fullSizeContentView)
        .ignoresSafeArea()
    }
}

extension View {
    // SwiftUI rejects menu(forEvent:) unless it thinks it owns the view at which
    // the click occurred. onDoubleClick makes a big NSView that fulfills this
    func akListContextMenu<MenuItems: View>(@ViewBuilder menuItems: () -> MenuItems) -> some View {
        self
            .onRawDoubleClick { }
            .contextMenu {
                menuItems()
            }
    }

    func akListOnDoubleClick(perform action: @escaping () -> Void) -> some View {
        // TODO
        self
            .onRawDoubleClick(handler: action)
    }
}