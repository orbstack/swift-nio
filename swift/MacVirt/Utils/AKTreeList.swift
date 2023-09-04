//
// Created by Danny Lin on 9/4/23.
//

import AppKit
import SwiftUI

// workaround for off-center disclosure arrow: https://stackoverflow.com/a/74894605
private class AKOutlineView: NSOutlineView {
    override func frameOfOutlineCell(atRow row: Int) -> NSRect {
        super.frameOfOutlineCell(atRow: row)
    }
}

protocol AKTreeListItem: Identifiable, Equatable {
    var listChildren: [any AKTreeListItem]? { get }
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

private struct AKTreeListNSView<Item: AKTreeListItem, ItemView: View>: NSViewRepresentable, Equatable {
    let items: [Item]
    let rowHeight: CGFloat
    let makeRowView: (Item) -> ItemView

    static func == (lhs: AKTreeListNSView, rhs: AKTreeListNSView) -> Bool {
        lhs.items == rhs.items && lhs.rowHeight == rhs.rowHeight
    }

    final class Coordinator: NSObject, NSOutlineViewDelegate {
        let parent: AKTreeListNSView
        @objc fileprivate dynamic var content: [AKTreeNode] = []
        var lastItems: [Item] = []

        private var objCache = [Item.ID: AKTreeNode]()
        private var objAccessTracker = [Item.ID]()

        init(_ parent: AKTreeListNSView) {
            self.parent = parent
        }

        // make views
        func outlineView(_ outlineView: NSOutlineView, viewFor tableColumn: NSTableColumn?, item: Any) -> NSView? {
            guard let nsNode = item as? NSTreeNode,
                  let node = nsNode.representedObject as? AKTreeNode else {
                return nil
            }

            let view = parent.makeRowView(node.value as! Item)
            return NSHostingView(rootView: view)
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
    }

    func makeNSView(context: Context) -> NSScrollView {
        let treeController = NSTreeController()
        treeController.bind(.contentArray, to: context.coordinator, withKeyPath: "content")
        treeController.objectClass = AKTreeNode.self
        treeController.childrenKeyPath = "children"
        treeController.countKeyPath = "childCount"
        treeController.leafKeyPath = "isLeaf"
        treeController.preservesSelection = true
        treeController.avoidsEmptySelection = false
        treeController.selectsInsertedObjects = false
        treeController.alwaysUsesMultipleValuesMarker = true // perf

        let outlineView = AKOutlineView()
        outlineView.delegate = context.coordinator
        outlineView.bind(.content, to: treeController, withKeyPath: "arrangedObjects")
        outlineView.bind(.selectionIndexPaths, to: treeController, withKeyPath: "selectionIndexPaths")
        // fix width changing when expanding/collapsing
        outlineView.autoresizesOutlineColumn = false
        outlineView.allowsMultipleSelection = true
        outlineView.allowsEmptySelection = true

        // hide header
        outlineView.headerView = nil

        // use outlineView's double click. more reliable than Swift onDoubleClick
        outlineView.target = context.coordinator
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
        guard items != coordinator.lastItems else {
            return
        }

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

struct AKTreeList<Item: AKTreeListItem, ItemView: View>: View {
    let items: [Item]
    let rowHeight: CGFloat
    let makeRowView: (Item) -> ItemView

    init(items: [Item], rowHeight: CGFloat, @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.items = items
        self.rowHeight = rowHeight
        self.makeRowView = makeRowView
    }

    var body: some View {
        AKTreeListNSView(items: items, rowHeight: rowHeight, makeRowView: makeRowView)
        // TODO: is this useless?
        .equatable()
        // fix toolbar color and blur (fullSizeContentView)
        .ignoresSafeArea()
    }
}