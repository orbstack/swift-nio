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
import Combine
import SwiftUI

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
    private var sort: Binding<AKSortDescriptor>? = nil
    private let rowHeight: CGFloat?
    private let columns: [AKColumn<Item, ItemView>]
    private var singleSelection = false
    private var flat = false
    private var expandByDefault = false
    private var autosaveName: String? = nil

    // hierarchical OR flat, with sections, with columns, multiple selection
    init(
        _ sections: [AKSection<Item>],
        selection: Binding<Set<Item.ID>>,
        sort: Binding<AKSortDescriptor>? = nil,
        rowHeight: CGFloat? = nil,
        flat: Bool = true,
        expandByDefault: Bool = false,
        autosaveName: String? = nil,
        columns: [AKColumn<Item, ItemView>]
    ) {
        self.sections = sections
        _selection = selection
        self.sort = sort
        self.rowHeight = rowHeight
        self.columns = columns
        self.flat = flat
        self.autosaveName = autosaveName
        self.expandByDefault = expandByDefault
    }

    var body: some View {
        AKTreeListImpl(
            envModel: envModel,
            sections: sections,
            rowHeight: rowHeight,
            singleSelection: singleSelection,
            isFlat: flat,
            expandByDefault: expandByDefault,
            autosaveName: autosaveName,
            defaultSort: sort?.wrappedValue,
            columns: columns
        )
        // fix toolbar color and blur (fullSizeContentView)
        .ignoresSafeArea()
        .onReceive(envModel.$selection) { selection in
            self.selection = selection as! Set<Item.ID>
        }
        .onReceive(envModel.$sort) { sort in
            if let sort {
                self.sort?.wrappedValue = sort
            }
        }
    }
}

// structs can't have convenience init, so use an extension
extension AKList {
    // hierarchical OR flat, with sections, with columns, single selection
    init(
        _ sections: [AKSection<Item>],
        selection singleBinding: Binding<Item.ID?>,
        rowHeight: CGFloat? = nil,
        flat: Bool = true,
        autosaveName: String? = nil,
        columns: [AKColumn<Item, ItemView>]
    ) {
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
            }
        )
        self.init(
            sections,
            selection: selBinding,
            rowHeight: rowHeight,
            flat: flat,
            autosaveName: autosaveName,
            columns: columns)
        singleSelection = true
    }

    // hierarchical OR flat, with sections, no columns, single selection
    init(
        _ sections: [AKSection<Item>],
        selection singleBinding: Binding<Item.ID?>,
        rowHeight: CGFloat? = nil,
        flat: Bool = true,
        autosaveName: String? = nil,
        @ViewBuilder makeRowView: @escaping (Item) -> ItemView
    ) {
        self.init(
            sections,
            selection: singleBinding,
            rowHeight: rowHeight,
            flat: flat,
            autosaveName: autosaveName,
            columns: AKColumn.single(makeRowView))
    }

    // hierarchical OR flat, with sections, no columns, multiple selection
    init(
        _ sections: [AKSection<Item>],
        selection: Binding<Set<Item.ID>>,
        rowHeight: CGFloat? = nil,
        flat: Bool = true,
        autosaveName: String? = nil,
        @ViewBuilder makeRowView: @escaping (Item) -> ItemView
    ) {
        self.init(
            sections,
            selection: selection,
            rowHeight: rowHeight,
            flat: flat,
            autosaveName: autosaveName,
            columns: AKColumn.single(makeRowView))
    }

    // hierarchical OR flat, no sections, multiple selection
    init(
        _ items: [Item],
        selection: Binding<Set<Item.ID>>,
        rowHeight: CGFloat? = nil,
        flat: Bool = true,
        autosaveName: String? = nil,
        @ViewBuilder makeRowView: @escaping (Item) -> ItemView
    ) {
        self.init(
            AKSection.single(items),
            selection: selection,
            rowHeight: rowHeight,
            flat: flat,
            autosaveName: autosaveName,
            columns: AKColumn.single(makeRowView))
    }

    // hierarchical OR flat, no sections, single selection
    init(
        _ items: [Item],
        selection singleBinding: Binding<Item.ID?>,
        rowHeight: CGFloat? = nil,
        flat: Bool = true,
        autosaveName: String? = nil,
        @ViewBuilder makeRowView: @escaping (Item) -> ItemView
    ) {
        self.init(
            AKSection.single(items),
            selection: singleBinding,
            rowHeight: rowHeight,
            flat: flat,
            autosaveName: autosaveName,
            makeRowView: makeRowView)
        singleSelection = true
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
            let view = view(atColumn: 0, row: targetRow, makeIfNecessary: false)
        {
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
                context: nil,  // deprecated
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
    let itemModel: AKListItemModel

    weak var outlineParent: AKOutlineView?
    var releaser: (() -> Void)?

    init(rootView: V, itemModel: AKListItemModel) {
        self.itemModel = itemModel
        super.init(rootView: rootView)
    }

    required init?(coder aDecoder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    required init(rootView: V) {
        fatalError("init(rootView:) has not been implemented")
    }

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
    @Published var sort: AKSortDescriptor? = nil
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

typealias AKListItemBase = Equatable & Identifiable

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

    func actuallyEqual<Item: AKListItemBase>(_ other: AKNode, itemType _: Item.Type) -> Bool {
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

    @ViewBuilder let makeCellView: (Item) -> ItemView

    var body: some View {
        if let item = itemModel.item {
            makeCellView(item as! Item)
                .environmentObject(envModel)
                .environmentObject(itemModel)
        } else {
            EmptyView()
        }
    }
}

private struct AKTreeListImpl<Item: AKListItem, ItemView: View>: NSViewRepresentable {
    typealias Section = AKSection<Item>
    typealias HostingView = AKHostingView<HostedItemView<Item, ItemView>>

    @ObservedObject var envModel: AKListModel

    let sections: [Section]
    let rowHeight: CGFloat?
    let singleSelection: Bool
    let isFlat: Bool
    let expandByDefault: Bool
    let autosaveName: String?
    let defaultSort: AKSortDescriptor?
    let columns: [AKColumn<Item, ItemView>]

    init(
        envModel: AKListModel,
        sections: [Section],
        rowHeight: CGFloat?,
        singleSelection: Bool,
        isFlat: Bool,
        expandByDefault: Bool,
        autosaveName: String?,
        defaultSort: AKSortDescriptor?,
        columns: [AKColumn<Item, ItemView>]
    ) {
        self.envModel = envModel
        self.sections = sections
        self.rowHeight = rowHeight
        self.singleSelection = singleSelection
        self.isFlat = isFlat
        self.expandByDefault = expandByDefault
        self.autosaveName = autosaveName
        self.defaultSort = defaultSort
        self.columns = columns
    }

    private final class AKTableColumn: NSTableColumn {
        let makeCellView: (Item) -> ItemView

        // preserve view identity to avoid losing state (e.g. popovers)
        var viewCache = [Item.ID: HostingView]()

        init(spec: AKColumn<Item, ItemView>) {
            self.makeCellView = spec.makeCellView
            super.init(identifier: NSUserInterfaceItemIdentifier(spec.id))
            self.headerCell.alignment = spec.alignment
        }

        required init(coder: NSCoder) {
            fatalError("init(coder:) has not been implemented")
        }
    }

    final class Coordinator: NSObject, NSOutlineViewDelegate, NSOutlineViewDataSource {
        var parent: AKTreeListImpl

        var rootNodes: [AKNode] = []
        var lastSections: [Section]?
        var firstContentfulUpdate = true

        // preserve objc object identity to avoid losing state
        // overriding isEqual would probably work but this is also good for perf
        private var objCache = [Item.ID: AKNode]()
        private var sectionCache = [String: AKNode]()
        // array is fastest since we just iterate and clear this
        private var objAccessTracker = [Item.ID]()
        private var sectionAccessTracker = [String]()

        init(_ parent: AKTreeListImpl) {
            self.parent = parent
        }

        /*
         * data source
         */
        func outlineView(_: NSOutlineView, numberOfChildrenOfItem item: Any?) -> Int {
            if let item = item as? AKNode {
                return item.children?.count ?? 0
            } else {
                return rootNodes.count
            }
        }

        func outlineView(_: NSOutlineView, isItemExpandable item: Any) -> Bool {
            if let item = item as? AKNode {
                return item.children != nil
            } else {
                return false
            }
        }

        func outlineView(_: NSOutlineView, child index: Int, ofItem item: Any?) -> Any {
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
        private func getOrCreateItemView(
            outlineView: NSOutlineView, itemId: Item.ID, column: AKTableColumn
        ) -> HostingView {
            // 1. cached for ID, to preserve identity
            if let view = column.viewCache[itemId] {
                return view
            }

            // 2. AppKit reuse queue
            // TODO: re-enable reuse (by uncommenting this). requires fixing a bug (especially obvious in Activity Monitor) where rows that frequently move around will cause some reused views to be invisible. I tried view.prepareForReuse(), custom and AppKit reuse queues, view.needsLayout=true, and view.layoutSubtreeIfNeeded(), to no avail.
            /*
            if let view = outlineView.makeView(withIdentifier: column.identifier, owner: nil) as? HostingView {
                return view
            }
            */

            // 3. make a new one
            let itemModel = AKListItemModel(itemId: itemId as AnyHashable)
            let swiftuiView = HostedItemView(
                envModel: parent.envModel,
                itemModel: itemModel,
                makeCellView: column.makeCellView)
            let nsView = AKHostingView(rootView: swiftuiView, itemModel: itemModel)
            // nsView.identifier = column.identifier // for reuse
            nsView.outlineParent = (outlineView as! AKOutlineView)
            column.viewCache[itemId] = nsView

            // set releaser
            nsView.releaser = { [weak column] in
                guard let column else { return }
                // remove from active cache
                column.viewCache.removeValue(forKey: itemId)
                // remove item
                itemModel.item = nil
            }

            return nsView
        }

        // make views
        func outlineView(
            _ outlineView: NSOutlineView, viewFor tableColumn: NSTableColumn?, item: Any
        )
            -> NSView?
        {
            let node = item as! AKNode
            switch node.type {
            case .item:
                let value = node.value as! Item
                let view = getOrCreateItemView(
                    outlineView: outlineView, itemId: value.id,
                    column: tableColumn as! AKTableColumn)

                // update value if needed
                // updateNSView does async so it's fine to update right here
                if (view.itemModel.item as? Item) != value {
                    view.itemModel.item = value
                }
                if (view.itemModel.itemId as? Item.ID) != value.id {
                    view.itemModel.itemId = value.id
                }

                return view

            case .section:
                // styling is applied by isGroupItem
                // TODO: if we put section items in children, and return isGroupItem=true, then theoretically AppKit should provide a sidebar-like show/hide arrow at the right. but I can't figure out how to make it do that (it just shows the normal disclosure triangle on the left)
                let cellView = NSTableCellView()
                let field = NSTextField(labelWithString: node.value as! String)
                cellView.textField = field  // necessary for isGroupItem to apply styling
                cellView.addSubview(field)  // necessary for text to be visible

                return cellView
            }
        }

        // at first glance this isn't needed because section nodes don't render selections,
        // but it still gets selected internally and breaks the rounding of adjacent rows
        func outlineView(_: NSOutlineView, shouldSelectItem item: Any) -> Bool {
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

        func outlineView(_: NSOutlineView, typeSelectStringFor _: NSTableColumn?, item: Any)
            -> String?
        {
            let item = item as! AKNode
            if item.type == .item {
                return (item.value as! Item).textLabel
            } else {
                return nil
            }
        }

        func outlineView(_ outlineView: NSOutlineView, persistentObjectForItem item: Any?) -> Any? {
            guard let item = item as? AKNode else { return nil }
            if item.type == .item {
                // returning swift obj breaks NSOutlineView
                // HACK: Swift hash values are diff across execs/runs due to seed
                return "\((item.value as! Item).id)"
            } else {
                return nil
            }
        }

        func outlineView(_ outlineView: NSOutlineView, itemForPersistentObject object: Any) -> Any?
        {
            guard let stringId = object as? String else { return nil }

            // scan root nodes for matching id hashValue
            // (we only support autosaving root node expansion)
            for node in objCache.values {
                if "\((node.value as! Item).id)" == stringId {
                    return node
                }
            }
            return nil
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

        func outlineView(_: NSOutlineView, isGroupItem item: Any) -> Bool {
            let item = item as! AKNode
            return item.type == .section
        }

        func outlineView(
            _ outlineView: NSOutlineView, userCanChangeVisibilityOf column: NSTableColumn
        ) -> Bool {
            // allow customizing all columns, if the header row is visible
            return true
        }

        func outlineViewSelectionDidChange(_ notification: Notification) {
            updateSelection(outlineView: notification.object as! NSOutlineView)
        }

        func outlineView(
            _ outlineView: NSOutlineView,
            sortDescriptorsDidChange oldDescriptors: [NSSortDescriptor]
        ) {
            if let newNsSort = outlineView.sortDescriptors.first,
                let nsSortKey = newNsSort.key
            {
                let newSort = AKSortDescriptor(columnId: nsSortKey, ascending: newNsSort.ascending)

                // "publishing changes from within view updates is not allowed"
                DispatchQueue.main.async { [parent] in
                    if parent.envModel.sort != newSort {
                        parent.envModel.sort = newSort
                    }
                }
            }
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
            if parent.envModel.selection != newSelection {
                parent.envModel.selection = newSelection
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
        // fixes layout of cell-based views (including section headers), doesn't break custom row heights
        outlineView.rowSizeStyle = .default

        // configure columns
        if columns.first?.title != nil {
            // show header
            outlineView.headerView = NSTableHeaderView()
            outlineView.usesAlternatingRowBackgroundColors = true
            outlineView.columnAutoresizingStyle = .reverseSequentialColumnAutoresizingStyle
            outlineView.autosaveTableColumns = true
        } else {
            // hide header
            outlineView.headerView = nil
        }
        if let defaultSort {
            outlineView.sortDescriptors = [
                NSSortDescriptor(key: defaultSort.columnId, ascending: defaultSort.ascending)
            ]
        }

        // add columns
        for column in columns {
            let nsColumn = AKTableColumn(spec: column)
            if let title = column.title {
                nsColumn.title = title
            }
            if let width = column.width {
                nsColumn.width = width
            }
            nsColumn.isEditable = false
            nsColumn.minWidth = 50
            // allow clicking to sort
            nsColumn.sortDescriptorPrototype = NSSortDescriptor(key: column.id, ascending: false)
            outlineView.addTableColumn(nsColumn)
        }

        // use outlineView's double click. more reliable than Swift onDoubleClick
        outlineView.target = coordinator
        outlineView.doubleAction = #selector(Coordinator.onDoubleClick)

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
            // restore table column state after initial update
            if let autosaveName {
                let outlineView = nsView.documentView as! NSOutlineView
                outlineView.autosaveName = autosaveName
            }
        } else {
            DispatchQueue.main.async { [self] in
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
        // TODO: add animations and fix reloading of children
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

        // restore expansion state after initial non-empty update
        // otherwise AppKit discards saved expansion state for items that are not present
        if coordinator.firstContentfulUpdate && !newNodes.isEmpty {
            if expandByDefault {
                outlineView.expandItem(nil, expandChildren: true)
            }

            outlineView.autosaveExpandedItems = true
            coordinator.firstContentfulUpdate = false
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
    func makeNSView(context _: Context) -> NSView {
        NSView(frame: .zero)
    }

    func updateNSView(_: NSView, context _: Context) {}
}

extension View {
    // SwiftUI rejects menu(forEvent:) unless it thinks it owns the view at which
    // the click occurred. add a big NSView background to fix it
    // overlay breaks tooltip hover
    func akListContextMenu<MenuItems: View>(@ViewBuilder menuItems: () -> MenuItems) -> some View {
        background { BoundingBoxOverlayView() }
            .contextMenu(menuItems: menuItems)
    }

    func akListOnDoubleClick(perform action: @escaping () -> Void) -> some View {
        modifier(DoubleClickViewModifier(action: action))
    }
}

struct AKColumn<Item: AKListItem, ItemView: View> {
    let id: String
    let title: String?
    let width: CGFloat?
    let alignment: NSTextAlignment
    let makeCellView: (Item) -> ItemView
}

extension AKColumn {
    static func single(@ViewBuilder _ makeCellView: @escaping (Item) -> ItemView) -> [AKColumn<
        Item, ItemView
    >] {
        [
            AKColumn(
                id: "column", title: nil, width: nil, alignment: .left, makeCellView: makeCellView)
        ]
    }
}

// TODO: which type can we put this on? AKList and AKColumn don't work because they can't infer ItemView for the static func call
func akColumn<Item: AKListItem>(
    id: String, title: String?, width: CGFloat? = nil, alignment: NSTextAlignment = .left,
    @ViewBuilder _ makeCellView: @escaping (Item) -> some View
) -> AKColumn<Item, AnyView> {
    AKColumn<Item, AnyView>(
        id: id, title: title, width: width, alignment: alignment,
        makeCellView: { AnyView(makeCellView($0)) })
}

struct AKSortDescriptor: Equatable {
    let columnId: String
    let ascending: Bool

    func compare<C: Comparable>(_ lhs: C, _ rhs: C) -> Bool {
        if ascending {
            return lhs < rhs
        } else {
            return lhs > rhs
        }
    }
}
