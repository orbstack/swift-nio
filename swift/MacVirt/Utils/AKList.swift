/*
 * Copyright (c) 2023 Orbital Labs, LLC <danny@orbstack.dev>
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

import AppKit
import SwiftUI
import Combine

private let maxReuseSlots = 2

// Hierarchical list using AppKit's NSOutlineView.
// Can also be used for non-hierarchical ("flat") lists.
//
// TO MIGRATE FROM SwiftUI List:
//   - List -> AKList
//   - Section -> [AKSection<Item>]
//   - .onRawDoubleClick -> .akListOnDoubleClick
//   - .contextMenu -> .akListContextMenu
//   - increase .vertical padding (4->8) to match SwiftUI List
//     * AKList doesn't add implicit padding
//   - add .environmentObjects to the item view
//     * needs to be reinjected across NSHostingView boundary
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
//   - native double click implementation for reliability
//   - sections for hierarchical lists
//   - hides empty sections
struct AKList<Item: AKListItem, ItemView: View>: View {
    @StateObject private var envModel = AKListModel()

    private let sections: [AKSection<Item>]
    @Binding private var selection: Set<Item.ID>
    private let rowHeight: CGFloat?
    private let makeRowView: (Item) -> ItemView
    private var singleSelection = false
    private var flat = false

    // hierarchical OR flat, with sections, multiple selection
    init(_ sections: [AKSection<Item>],
         selection: Binding<Set<Item.ID>>,
         rowHeight: CGFloat? = nil,
         flat: Bool = true,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.sections = sections
        self._selection = selection
        self.rowHeight = rowHeight
        self.makeRowView = makeRowView
        self.flat = flat
    }

    var body: some View {
        AKTreeListImpl(envModel: envModel,
                sections: sections,
                rowHeight: rowHeight,
                singleSelection: singleSelection,
                isFlat: flat,
                makeRowView: makeRowView)
                // fix toolbar color and blur (fullSizeContentView)
        .ignoresSafeArea()
        .onReceive(envModel.$selection) { selection in
            self.selection = selection as! Set<Item.ID>
        }
    }
}

// structs can't have convenience init, so use an extension
extension AKList {
    // hierarchical OR flat, with sections, single selection
    init(_ sections: [AKSection<Item>],
         selection singleBinding: Binding<Item.ID?>,
         rowHeight: CGFloat? = nil,
         flat: Bool = true,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        let selBinding = Binding<Set<Item.ID>>(
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
        self.init(sections,
                selection: selBinding,
                rowHeight: rowHeight,
                flat: flat,
                makeRowView: makeRowView)
        self.singleSelection = true
    }

    // hierarchical OR flat, no sections, multiple selection
    init(_ items: [Item],
         selection: Binding<Set<Item.ID>>,
         rowHeight: CGFloat? = nil,
         flat: Bool = true,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.init(AKSection.single(items),
                selection: selection,
                rowHeight: rowHeight,
                flat: flat,
                makeRowView: makeRowView)
    }

    // hierarchical OR flat, no sections, single selection
    init(_ items: [Item],
         selection singleBinding: Binding<Item.ID?>,
         rowHeight: CGFloat? = nil,
         flat: Bool = true,
         @ViewBuilder makeRowView: @escaping (Item) -> ItemView) {
        self.init(AKSection.single(items),
                selection: singleBinding,
                rowHeight: rowHeight,
                flat: flat,
                makeRowView: makeRowView)
        self.singleSelection = true
    }
}

private class AKOutlineView: NSOutlineView {
    // workaround for off-center disclosure arrow: https://stackoverflow.com/a/74894605
    override func frameOfOutlineCell(atRow row: Int) -> NSRect {
        super.frameOfOutlineCell(atRow: row)
    }

    // we get here if the right click wasn't handled by SwiftUI, usually b/c out of bounds
    // e.g. clicked on arrow or margin
    // never use the fake menu, but try to forward it
    override func menu(for event: NSEvent) -> NSMenu? {
        // calling super.menu makes highlight ring appear, so do this ourselves
        // otherwise right-clicking section header triggers ring
        let targetRow = row(at: convert(event.locationInWindow, from: nil))

        // find the clicked view
        if targetRow != -1,
           let view = self.view(atColumn: 0, row: targetRow, makeIfNecessary: false) {
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
    var releaser: (() -> Void)?

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

    override func viewDidMoveToSuperview() {
        super.viewDidMoveToSuperview()
        if superview == nil {
            releaser?()
        }
    }
}

// type-erased to make env-based view modifiers feasible
class AKListModel: ObservableObject {
    let doubleClicks = PassthroughSubject<AnyHashable, Never>()
    @Published var selection: Set<AnyHashable> = []
}

private class AKListItemModel: ObservableObject {
    @Published var item: (any AKListItem)?
    @Published var itemId: AnyHashable

    init(itemId: AnyHashable) {
        self.itemId = itemId
    }
}

private class CachedViewHolder<V: View> {
    var view: AKHostingView<V>
    var model: AKListItemModel

    init(view: AKHostingView<V>, model: AKListItemModel) {
        self.view = view
        self.model = model
    }
}

typealias AKListItemBase = Identifiable & Equatable

protocol AKListItem: AKListItemBase {
    var listChildren: [any AKListItem]? { get }
    var textLabel: String? { get }
}

extension AKListItem {
    var listChildren: [any AKListItem]? {
        nil
    }

    var textLabel: String? {
        nil
    }
}

struct AKSection<Element: AKListItem>: AKListItemBase {
    // nil = no header
    let title: String?
    let items: [Element]

    var id: String? {
        title
    }

    init(_ title: String?, _ items: [Element]) {
        self.title = title
        self.items = items
    }

    static func single(_ items: [Element]) -> [AKSection<Element>] {
        [AKSection(nil, items)]
    }
}

private enum AKNodeType {
    case section
    case item
}

private class AKNode: NSObject {
    let type: AKNodeType
    var children: [AKNode]?
    var value: any Equatable

    func actuallyEqual<Item: AKListItemBase>(_ other: AKNode, itemType: Item.Type) -> Bool {
        switch (type, other.type) {
        case (.section, .section):
            return value as! String == other.value as! String
        case (.item, .item):
            return value as! Item == other.value as! Item
        default:
            return false
        }
    }

    init(type: AKNodeType, value: any Equatable) {
        self.type = type
        self.value = value
    }
}

private struct HostedItemView<Item: AKListItem, ItemView: View>: View {
    @ObservedObject var envModel: AKListModel
    @ObservedObject var itemModel: AKListItemModel

    @ViewBuilder let makeRowView: (Item) -> ItemView

    var body: some View {
        if let item = itemModel.item {
            makeRowView(item as! Item)
            .environmentObject(envModel)
            .environmentObject(itemModel)
        } else {
            EmptyView()
        }
    }
}

private struct AKTreeListImpl<Item: AKListItem, ItemView: View>: NSViewRepresentable {
    typealias Section = AKSection<Item>
    typealias CachedView = CachedViewHolder<HostedItemView<Item, ItemView>>

    @ObservedObject var envModel: AKListModel

    let sections: [Section]
    let rowHeight: CGFloat?
    let singleSelection: Bool
    let isFlat: Bool
    let makeRowView: (Item) -> ItemView

    final class Coordinator: NSObject, NSOutlineViewDelegate, NSOutlineViewDataSource {
        var parent: AKTreeListImpl

        var rootNodes: [AKNode] = []
        var lastSections: [Section]?

        // preserve objc object identity to avoid losing state
        // overriding isEqual would probably work but this is also good for perf
        private var objCache = [Item.ID: AKNode]()
        private var sectionCache = [String: AKNode]()
        // array is fastest since we just iterate and clear this
        private var objAccessTracker = [Item.ID]()
        private var sectionAccessTracker = [String]()

        // preserve view identity to avoid losing state (e.g. popovers)
        private var viewCache = [Item.ID: CachedView]()
        // custom reuse queue. hard to use nibs, and we need the identity-preserving cache logic too
        private var reuseQueue = [CachedView]()

        init(_ parent: AKTreeListImpl) {
            self.parent = parent
            reuseQueue.reserveCapacity(maxReuseSlots)
        }

        /*
         * data source
         */
        func outlineView(_ outlineView: NSOutlineView, numberOfChildrenOfItem item: Any?) -> Int {
            if let item = item as? AKNode {
                return item.children?.count ?? 0
            } else {
                return rootNodes.count
            }
        }

        func outlineView(_ outlineView: NSOutlineView, isItemExpandable item: Any) -> Bool {
            if let item = item as? AKNode {
                return item.children != nil
            } else {
                return false
            }
        }

        func outlineView(_ outlineView: NSOutlineView, child index: Int, ofItem item: Any?) -> Any {
            if item == nil {
                return rootNodes[index]
            } else if let item = item as? AKNode {
                return item.children![index]
            } else {
                fatalError("invalid item")
            }
        }

        /*
         * delegate
         */
        private func getOrCreateItemView(outlineView: NSOutlineView, itemId: Item.ID) -> CachedView {
            // 1. cached for ID, to preserve identity
            if let holder = viewCache[itemId] {
                return holder
            }

            // 2. look for reusable one
            if let holder = reuseQueue.popLast() {
                // a reused view should be added back to the cache once it's been rebound
                viewCache[itemId] = holder
                return holder
            }

            // 3. make a new one
            let itemModel = AKListItemModel(itemId: itemId as AnyHashable)
            // doing .environmentObject in the SwiftUI view lets us avoid AnyView here
            let hostedView = HostedItemView(envModel: parent.envModel,
                    itemModel: itemModel,
                    makeRowView: parent.makeRowView)
            let nsView = AKHostingView(rootView: hostedView)
            nsView.outlineParent = (outlineView as! AKOutlineView)

            let holder = CachedView(view: nsView, model: itemModel)
            viewCache[itemId] = holder

            // set releaser
            nsView.releaser = { [weak self, weak holder] in
                guard let self, let holder else { return }
                // remove from active cache
                self.viewCache.removeValue(forKey: holder.model.itemId as! Item.ID)
                // add to reuse queue if space is available
                if self.reuseQueue.count < maxReuseSlots {
                    self.reuseQueue.append(holder)
                }
                // remove item
                holder.model.item = nil
            }

            return holder
        }

        // make views
        func outlineView(_ outlineView: NSOutlineView, viewFor tableColumn: NSTableColumn?, item: Any) -> NSView? {
            let node = item as! AKNode
            if node.type == .item {
                let value = node.value as! Item
                let holder = getOrCreateItemView(outlineView: outlineView, itemId: value.id)

                // update value if needed
                // updateNSView does async so it's fine to update right here
                if (holder.model.item as? Item) != value {
                    holder.model.item = value
                }
                if (holder.model.itemId as? Item.ID) != value.id {
                    holder.model.itemId = value.id
                }

                return holder.view
            } else if node.type == .section {
                // pixel-perfect match of SwiftUI default section header
                let cellView = NSTableCellView()
                let field = NSTextField(labelWithString: node.value as! String)
                field.font = NSFont.systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold)
                field.textColor = .secondaryLabelColor
                field.isEditable = false
                cellView.addSubview(field)

                // center vertically, align to left
                field.translatesAutoresizingMaskIntoConstraints = false
                NSLayoutConstraint.activate([
                    field.leadingAnchor.constraint(equalTo: cellView.leadingAnchor),
                    field.centerYAnchor.constraint(equalTo: cellView.centerYAnchor),
                ])

                return cellView
            } else {
                return nil
            }
        }

        // at first glance this isn't needed because section nodes don't render selections,
        // but it still gets selected internally and breaks the rounding of adjacent rows
        func outlineView(_ outlineView: NSOutlineView, shouldSelectItem item: Any) -> Bool {
            let item = item as! AKNode
            return item.type == .item
        }

        func outlineView(_ outlineView: NSOutlineView, heightOfRowByItem item: Any) -> CGFloat {
            let item = item as! AKNode
            if item.type == .item {
                return parent.rowHeight ?? outlineView.rowHeight
            } else if item.type == .section {
                // match SwiftUI section
                return 28
            } else {
                return 0
            }
        }

        func outlineView(_ outlineView: NSOutlineView, typeSelectStringFor tableColumn: NSTableColumn?, item: Any) -> String? {
            let item = item as! AKNode
            if item.type == .item {
                return (item.value as! Item).textLabel
            } else {
                return nil
            }
        }

        @objc func onDoubleClick(_ sender: Any) {
            // expand or collapse row
            let outlineView = sender as! NSOutlineView
            let row = outlineView.clickedRow
            guard row != -1 else {
                return
            }

            let item = outlineView.item(atRow: row) as? AKNode
            guard let item else { return }
            if outlineView.isItemExpanded(item) {
                outlineView.animator().collapseItem(item)
            } else {
                outlineView.animator().expandItem(item)
            }

            // emit double click event via notification center
            if item.type == .item {
                let value = item.value as! Item
                parent.envModel.doubleClicks.send(value.id as AnyHashable)
            }
        }

        private func mapNode(item: Item) -> AKNode {
            var node: AKNode
            if let cachedNode = objCache[item.id] {
                node = cachedNode
            } else {
                node = AKNode(type: .item, value: item)
                objCache[item.id] = node
            }
            objAccessTracker.append(item.id)

            var nodeChildren = item.listChildren?.map { mapNode(item: $0 as! Item) }
            // map empty to nil
            if nodeChildren?.isEmpty ?? false {
                nodeChildren = nil
            }

            // do we need to update this node? if not, avoid triggering NSTreeController's KVO
            // isLeaf and count are derived from children, so no need to check
            node.children = nodeChildren
            node.value = item
            return node
        }

        private func mapSectionNode(title: String) -> AKNode {
            var node: AKNode
            if let cachedNode = sectionCache[title] {
                node = cachedNode
            } else {
                node = AKNode(type: .section, value: title)
                sectionCache[title] = node
            }
            sectionAccessTracker.append(title)
            return node
        }

        func mapAllNodes(sections: [Section]) -> [AKNode] {
            // record accessed nodes
            let newNodes = sections.flatMap {
                // don't show empty sections
                if $0.items.isEmpty {
                    return [AKNode]()
                }

                // more efficient than map and concat
                var sectionNodes = [AKNode]()
                sectionNodes.reserveCapacity($0.items.count + 1)
                if let title = $0.title {
                    // TODO: if we use children, then groups are collapsible
                    sectionNodes.append(mapSectionNode(title: title))
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

            // remove unused sections
            let unusedSections = sectionCache.filter { !sectionAccessTracker.contains($0.key) }
            for (title, _) in unusedSections {
                sectionCache.removeValue(forKey: title)
            }

            // clear access tracker
            objAccessTracker.removeAll()
            sectionAccessTracker.removeAll()
            return newNodes
        }

        func outlineView(_ outlineView: NSOutlineView, isGroupItem item: Any) -> Bool {
            let item = item as! AKNode
            return item.type == .section
        }

        func outlineViewSelectionDidChange(_ notification: Notification) {
            updateSelection(outlineView: notification.object as! NSOutlineView)
        }

        func updateSelection(outlineView: NSOutlineView) {
            // Publishing changes from within view updates is not allowed, this will cause undefined behavior.
            // however, we only call this from either AppKit click event or .async in case of completeUpdate, so it's ok
            let selectedIds = outlineView.selectedRowIndexes
                .compactMap {
                    let item = outlineView.item(atRow: $0) as? AKNode
                    if item?.type == .item {
                        return (item?.value as! Item).id as AnyHashable
                    } else {
                        return nil
                    }
                }

            let newSelection = Set(selectedIds)
            if self.parent.envModel.selection != newSelection {
                self.parent.envModel.selection = newSelection
            }
        }
    }

    func makeNSView(context: Context) -> NSScrollView {
        let coordinator = context.coordinator
        coordinator.parent = self

        let outlineView = AKOutlineView()
        outlineView.delegate = coordinator
        outlineView.dataSource = coordinator
        // fix width changing when expanding/collapsing
        outlineView.autoresizesOutlineColumn = false
        outlineView.allowsMultipleSelection = !singleSelection
        outlineView.allowsEmptySelection = true
        if let rowHeight {
            outlineView.rowHeight = rowHeight
        } else {
            outlineView.usesAutomaticRowHeights = true
        }
        if isFlat {
            // remove padding at left
            outlineView.indentationPerLevel = 0
        }
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

        // convert to nodes
        // DispatchQueue.main.async causes initial flicker,
        // but later we need it to avoid AttributeGraph cycles when clicking popovers during updates
        // because updating .content updates SwiftUI hosting views, but updateNSView is called inside a SwiftUI view update
        // this makes the updating non-atomic but it's fine
        if coordinator.lastSections == nil {
            completeUpdate(coordinator: coordinator, nsView: nsView)
        } else {
            DispatchQueue.main.async {
                completeUpdate(coordinator: coordinator, nsView: nsView)
            }
        }
        coordinator.lastSections = sections
    }

    private func completeUpdate(coordinator: Coordinator, nsView: NSScrollView) {
        let newNodes = coordinator.mapAllNodes(sections: sections)
        let outlineView = nsView.documentView as! NSOutlineView

        // save selection, identity-based
        let selectedItems = outlineView.selectedRowIndexes
            .map { outlineView.item(atRow: $0) as! AKNode }

        // update
        // TODO add animations and fix reloading of children
        coordinator.rootNodes = newNodes
        outlineView.reloadData()

        // restore selection
        let selectedRows = selectedItems.compactMap {
            let row = outlineView.row(forItem: $0)
            return row == -1 ? nil : row
        }
        outlineView.selectRowIndexes(IndexSet(selectedRows), byExtendingSelection: false)

        // update selection to account for deleted items
        coordinator.updateSelection(outlineView: outlineView)

        // if no selected items are on screen, scroll to the first one
        // (doesn't work with insert/remove animation)
        let visibleRows = outlineView.rows(in: outlineView.visibleRect)
        if !selectedRows.contains(where: { visibleRows.contains($0) }) {
            if let firstRow = selectedRows.first {
                NSAnimationContext.runAnimationGroup { context in
                    context.allowsImplicitAnimation = true
                    outlineView.scrollRowToVisible(firstRow)
                }
            }
        }
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(self)
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

private struct BoundingBoxOverlayView: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView {
        NSView(frame: .zero)
    }

    func updateNSView(_ nsView: NSView, context: Context) {
    }
}

extension View {
    // SwiftUI rejects menu(forEvent:) unless it thinks it owns the view at which
    // the click occurred. add a big NSView overlay to fix it
    func akListContextMenu<MenuItems: View>(@ViewBuilder menuItems: () -> MenuItems) -> some View {
        self
            .overlay { BoundingBoxOverlayView() }
            .contextMenu(menuItems: menuItems)
    }

    func akListOnDoubleClick(perform action: @escaping () -> Void) -> some View {
        self.modifier(DoubleClickViewModifier(action: action))
    }
}
