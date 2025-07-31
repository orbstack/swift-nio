import Defaults
import QuickLookUI
import SwiftUI
import UniformTypeIdentifiers
import _NIOFileSystem

private let tableCellIdentifier = NSUserInterfaceItemIdentifier("tableCell")

extension NSPasteboard.PasteboardType {
    static let nodeRowPasteboardType = NSPasteboard.PasteboardType(
        "dev.orbstack.OrbStack.nodeRowPasteboardType")
}

struct FileManagerView: View {
    @StateObject private var model = FileManagerOutlineDelegate()
    @State private var sort = AKSortDescriptor(columnId: Columns.name, ascending: true)
    @State private var selection: Set<String> = []

    let rootPath: String

    var body: some View {
        FileManagerNSView(delegate: model)
            .onChange(of: rootPath, initial: true) { _, newRootPath in
                Task {
                    do {
                        try await model.loadRoot(rootPath: newRootPath)
                    } catch {
                        NSLog("Error loading files: \(error)")
                    }
                }
            }
    }
}

private struct FileManagerNSView: NSViewRepresentable {
    let delegate: FileManagerOutlineDelegate

    func makeNSView(context: Context) -> NSScrollView {
        let view = FileManagerOutlineView()

        delegate.view = view
        view.delegate = delegate  // weak
        view.dataSource = delegate  // weak

        view.setDraggingSourceOperationMask([.copy, .delete], forLocal: false)
        view.registerForDraggedTypes(
            NSFilePromiseReceiver.readableDraggedTypes.map { NSPasteboard.PasteboardType($0) })
        view.registerForDraggedTypes([.nodeRowPasteboardType, .fileURL])

        // dummy menu to trigger highlight
        view.menu = NSMenu()
        view.headerView = NSTableHeaderView()
        view.usesAlternatingRowBackgroundColors = true
        view.allowsMultipleSelection = true
        view.autoresizesOutlineColumn = false
        view.rowHeight = 20 // match row height in Finder

        view.sortDescriptors = [NSSortDescriptor(key: Columns.name, ascending: true)]

        view.target = delegate
        view.doubleAction = #selector(FileManagerOutlineDelegate.onDoubleClick)
        view.columnAutoresizingStyle = .firstColumnOnlyAutoresizingStyle

        let colName = NSTableColumn(identifier: .init(Columns.name))
        colName.title = "Name"
        colName.isEditable = true
        colName.sortDescriptorPrototype = NSSortDescriptor(key: Columns.name, ascending: true)
        colName.minWidth = 100
        view.addTableColumn(colName)

        let colModified = NSTableColumn(identifier: .init(Columns.modified))
        colModified.title = "Date Modified"
        colModified.isEditable = false
        colModified.sortDescriptorPrototype = NSSortDescriptor(
            key: Columns.modified, ascending: false)
        colModified.minWidth = 80
        colModified.width = 160
        colModified.maxWidth = 200
        view.addTableColumn(colModified)

        let colSize = NSTableColumn(identifier: .init(Columns.size))
        colSize.title = "Size"
        colSize.isEditable = false
        colSize.sortDescriptorPrototype = NSSortDescriptor(key: Columns.size, ascending: false)
        colSize.minWidth = 30
        colSize.width = 70
        colSize.maxWidth = 120
        view.addTableColumn(colSize)

        let colType = NSTableColumn(identifier: .init(Columns.type))
        colType.title = "Kind"
        colType.isEditable = false
        colType.sortDescriptorPrototype = NSSortDescriptor(key: Columns.type, ascending: false)
        colType.minWidth = 30
        colType.width = 70
        colType.maxWidth = 120
        view.addTableColumn(colType)

        let scrollView = NSScrollView()
        scrollView.documentView = view
        scrollView.hasVerticalScroller = true
        return scrollView
    }

    func updateNSView(_ nsView: NSScrollView, context: Context) {
    }
}

private class FileManagerOutlineView: NSOutlineView, QLPreviewPanelDataSource, QLPreviewPanelDelegate {
    // open quick look on space key pressed
    override func keyDown(with event: NSEvent) {
        if event.charactersIgnoringModifiers == " " {
            QLPreviewPanel.shared()?.makeKeyAndOrderFront(nil)
        } else {
            // Pass other key events to the outline view for navigation, selection, etc.
            super.keyDown(with: event)
        }
    }

    // nsresponder commands
    @objc func copy(_ sender: Any?) {
        copyAction(actionIndexes: selectedRowIndexes)
    }

    @objc func paste(_ sender: Any?) {
        pasteAction(actionIndexes: selectedRowIndexes)
    }

    func numberOfPreviewItems(in panel: QLPreviewPanel!) -> Int {
        // number of selected items
        return self.selectedRowIndexes.count
    }

    func previewPanel(_ panel: QLPreviewPanel!, previewItemAt index: Int) -> QLPreviewItem! {
        let viewIndex = Array(self.selectedRowIndexes)[index]
        let item = self.item(atRow: viewIndex) as! FileItem
        return FileManagerPreviewItem(outlineItem: item)
    }

    func previewPanel(
        _ panel: QLPreviewPanel!,
        handle event: NSEvent!
    ) -> Bool {
        // Pass all unhandled Quick Look events to the outline view
        // for up/down arrow, etc
        switch event.type {
        case .keyDown:
            self.keyDown(with: event)
            return true
        case .keyUp:
            self.keyUp(with: event)
            return true
        case .flagsChanged:
            self.flagsChanged(with: event)
            return true
        default:
            return false
        }
    }

    func previewPanel(_ panel: QLPreviewPanel!, sourceFrameOnScreenFor item: (any QLPreviewItem)!) -> NSRect {
        // find the frame of the item
        guard let item = item as? FileManagerPreviewItem else {
            return .zero
        }
        let row = self.row(forItem: item.outlineItem)
        guard row != -1 else {
            return .zero
        }

        if let itemView = self.view(atColumn: 0, row: row, makeIfNecessary: false) as? TextFieldCellView,
            let imageView = itemView.imageView,
            let window = itemView.window {
            let frameInWindow = itemView.convert(imageView.frame, to: nil)
            return window.convertToScreen(frameInWindow)
        } else {
            return .zero
        }
    }

    func previewPanel(
        _ panel: QLPreviewPanel!,
        transitionImageFor item: (any QLPreviewItem)!,
        contentRect: UnsafeMutablePointer<NSRect>!
    ) -> Any! {
        guard let item = item as? FileManagerPreviewItem else {
            return nil
        }

        return item.outlineItem.icon
    }

    override func acceptsPreviewPanelControl(_ panel: QLPreviewPanel!) -> Bool {
        return true
    }

    override func beginPreviewPanelControl(_ panel: QLPreviewPanel!) {
        panel.dataSource = self
        panel.delegate = self
    }

    override func endPreviewPanelControl(_ panel: QLPreviewPanel!) {
        panel.dataSource = nil
        panel.delegate = nil
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        // trigger border highlight
        super.menu(for: event)

        let actionIndexes =
            selectedRowIndexes.contains(clickedRow) ? [clickedRow] : selectedRowIndexes
        return RIMenu {
            if actionIndexes.count >= 1 {
                RIMenuItem("Open") { [self] in
                    for index in actionIndexes {
                        if let item = item(atRow: index) as? FileItem {
                            NSWorkspace.shared.open(URL(filePath: item.path))
                        }
                    }
                }

                RIMenuItem.separator()

                RIMenuItem("Copy") {
                    self.copyAction(actionIndexes: actionIndexes)
                }
            }

            if actionIndexes.count <= 1 {
                RIMenuItem("Paste") {
                    self.pasteAction(actionIndexes: actionIndexes)
                }
            }

            RIMenuItem.separator()

            if actionIndexes.count == 1 {
                RIMenuItem("Show in Finder") { [self] in
                    for index in actionIndexes {
                        if let item = item(atRow: index) as? FileItem {
                            NSWorkspace.shared.selectFile(
                                item.path, inFileViewerRootedAtPath: item.path)
                        }
                    }
                }

                RIMenuItem("Quick Look") {
                    QLPreviewPanel.shared()?.makeKeyAndOrderFront(nil)
                }
            }
        }.menu
    }

    private func copyAction(actionIndexes: IndexSet) {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        for index in actionIndexes {
            if let item = item(atRow: index) as? FileItem {
                pasteboard.writeObjects([URL(filePath: item.path) as NSURL])
            }
        }
    }

    private func pasteAction(actionIndexes: IndexSet) {
        var destinationPath: URL
        if actionIndexes.count == 1 {
            let selectedFile = item(atRow: actionIndexes.first!) as! FileItem
            destinationPath = URL(filePath: selectedFile.path)
            if selectedFile.type != .directory {
                destinationPath = destinationPath.deletingLastPathComponent()
            }
        } else if selectedRowIndexes.count == 0 {
            if let delegate = self.delegate as? FileManagerOutlineDelegate,
                let rootPath = delegate.rootPath
            {
                destinationPath = URL(filePath: rootPath)
            } else {
                return
            }
        } else {
            return
        }

        let pasteboard = NSPasteboard.general
        guard let items = pasteboard.readObjects(forClasses: [NSURL.self], options: nil) as? [NSURL]
        else { return }
        for item in items {
            do {
                if let lastPathComponent = item.lastPathComponent {
                    try FileManager.default.copyItem(
                        at: item as URL,
                        to: destinationPath.appendingPathComponent(lastPathComponent))
                } else {
                    NSLog("Error copying item: \(item) has no last path component")
                }
            } catch {
                NSLog("Error copying item: \(error)")
            }
        }
    }

    @objc func openInFinderItem() {
        // openInFinderItem is only called if a single item is selected
        let item = item(atRow: selectedRowIndexes.first!) as! FileItem
        NSWorkspace.shared.open(URL(filePath: item.path))
    }
}

private class FileManagerPreviewItem: NSObject, QLPreviewItem {
    let outlineItem: FileItem
    var previewItemURL: URL!

    init(outlineItem: FileItem) {
        self.outlineItem = outlineItem
        super.init()
        previewItemURL = URL(filePath: outlineItem.path)
    }
}

private class FileManagerOutlineDelegate: NSObject, NSOutlineViewDelegate, NSOutlineViewDataSource,
    ObservableObject
{
    var view: NSOutlineView!
    var rootPath: String?

    private var sortDesc = AKSortDescriptor(columnId: Columns.name, ascending: true)

    private var expandedPaths = Set<String>()
    private var rootItems: [FileItem] = []

    private func getDirectoryItems(path: String) async throws -> [FileItem] {
        var items = [FileItem]()
        try await FileSystem.shared.withDirectoryHandle(atPath: FilePath(path)) { dir in
            for try await file in dir.listContents() {
                guard
                    let info = try await FileSystem.shared.info(
                        forFileAt: file.path, infoAboutSymbolicLink: true)
                else {
                    continue
                }

                let path = file.path.string
                let icon = NSWorkspace.shared.icon(forFile: path)
                let item = FileItem(
                    path: path, name: file.name.string, type: FileItemType(from: file.type),
                    size: info.size, modified: info.lastDataModificationTime.date, icon: icon,
                    children: file.type == .directory ? [] : nil)
                if file.type == .directory && expandedPaths.contains(path) {
                    item.children = try await getDirectoryItems(path: path)
                }

                items.append(item)
            }
        }

        switch sortDesc.columnId {
        case Columns.name:
            items.sort { $0.name < $1.name }
        case Columns.modified:
            items.sort { $0.modified < $1.modified }
        case Columns.size:
            items.sort { $0.size < $1.size }
        case Columns.type:
            items.sort { $0.type < $1.type }
        default:
            break
        }

        return items
    }

    @MainActor
    func loadRoot(rootPath: String) async throws {
        expandedPaths.removeAll()
        self.rootPath = rootPath
        self.rootItems = try await getDirectoryItems(path: rootPath)
        view.reloadData()
    }

    @MainActor
    func expandItem(item: FileItem) async throws {
        expandedPaths.insert(item.path)
        item.children = try await getDirectoryItems(path: item.path)
    }

    func collapseItem(item: FileItem) {
        expandedPaths.remove(item.path)
    }

    func reSort() {
        rootItems.sort(desc: sortDesc)
        view.reloadData()
    }

    // MARK: - dataSrc
    func outlineView(_ outlineView: NSOutlineView, numberOfChildrenOfItem item: Any?) -> Int {
        if let item = item as? FileItem {
            return item.children?.count ?? 0
        } else {
            return rootItems.count
        }
    }

    func outlineView(_ outlineView: NSOutlineView, isItemExpandable item: Any) -> Bool {
        if let item = item as? FileItem {
            return item.children != nil
        } else {
            return false
        }
    }

    func outlineView(_ outlineView: NSOutlineView, child index: Int, ofItem item: Any?) -> Any {
        if item == nil {
            return rootItems[index]
        } else if let item = item as? FileItem {
            return item.children![index]
        } else {
            fatalError("invalid item")
        }
    }

    // MARK: - delegat
    func outlineView(_ outlineView: NSOutlineView, viewFor tableColumn: NSTableColumn?, item: Any)
        -> NSView?
    {
        let item = item as! FileItem
        switch tableColumn?.identifier.rawValue {
        case Columns.name:
            return TextFieldCellView(value: item.name, editable: true, image: item.icon)
        case Columns.modified:
            return TextFieldCellView(
                value: item.modified.formatted(date: .abbreviated, time: .shortened),
                color: .secondaryLabelColor)
        case Columns.size:
            if item.type == .regular {
                return TextFieldCellView(
                    value: item.size.formatted(.byteCount(style: .file)),
                    color: .secondaryLabelColor, tabularNums: true)
            } else {
                return nil
            }
        case Columns.type:
            return TextFieldCellView(
                value: item.type.description, color: .secondaryLabelColor)
        default:
            return nil
        }
    }
    // func outlineView(_ outlineView: NSOutlineView, objectValueFor tableColumn: NSTableColumn?, byItem item: Any?) -> Any? {
    //     let item = item as! FileItem
    //     switch tableColumn?.identifier.rawValue {
    //     case Columns.name:
    //         print("supply name: \(item.name)")
    //         return item.name
    //     case Columns.modified:
    //         return item.modified.formatted(date: .abbreviated, time: .shortened)
    //     case Columns.size:
    //         if item.type == .regular {
    //             return item.size.formatted(.byteCount(style: .file))
    //         } else {
    //             return nil
    //         }
    //     case Columns.type:
    //         return item.type.description
    //     default:
    //     print("unknown col")
    //         return nil
    //     }
    // }

    func outlineView(
        _ outlineView: NSOutlineView, typeSelectStringFor tableColumn: NSTableColumn?, item: Any
    ) -> String? {
        if let item = item as? FileItem {
            return item.name
        } else {
            return nil
        }
    }

    func outlineView(
        _ outlineView: NSOutlineView, userCanChangeVisibilityOf column: NSTableColumn
    ) -> Bool {
        return column.identifier.rawValue != Columns.name
    }

    func outlineView(
        _ outlineView: NSOutlineView,
        sortDescriptorsDidChange oldDescriptors: [NSSortDescriptor]
    ) {
        if let newNsSort = outlineView.sortDescriptors.first,
            let nsSortKey = newNsSort.key
        {
            let newSort = AKSortDescriptor(columnId: nsSortKey, ascending: newNsSort.ascending)
            sortDesc = newSort
            reSort()
        }
    }

    //    func outlineView(_ outlineView: NSOutlineView, pasteboardWriterForItem item: Any) -> NSPasteboardWriting? {
    //    }

    func outlineViewItemWillExpand(_ notification: Notification) {
        if let node = notification.userInfo?["NSObject"] as? FileItem {
            Task {
                do {
                    try await expandItem(item: node)
                } catch {
                    NSLog("Error expanding item: \(error)")
                }
            }

            print("expanding \(node.name)")
            view.beginUpdates()
            view.insertItems(
                at: IndexSet(integersIn: 0..<node.children!.count), inParent: node,
                withAnimation: .effectGap)
            view.endUpdates()
        }
    }

    func outlineViewItemWillCollapse(_ notification: Notification) {
        if let node = notification.userInfo?["NSObject"] as? FileItem {
            view.beginUpdates()
            view.removeItems(
                at: IndexSet(integersIn: 0..<node.children!.count), inParent: node,
                withAnimation: .effectGap)
            view.endUpdates()

            collapseItem(item: node)
        }
    }

    @objc func onDoubleClick(_ sender: NSOutlineView) {
        // expand or collapse row
        let row = sender.clickedRow
        guard row != -1 else {
            return
        }

        let item = sender.item(atRow: row) as? FileItem
        guard let item else { return }
        if item.type == .directory {
            if sender.isItemExpanded(item) {
                sender.animator().collapseItem(item)
            } else {
                sender.animator().expandItem(item)
            }
        } else {
            let actionIndexes = sender.selectedRowIndexes.contains(row) ? sender.selectedRowIndexes : IndexSet(integer: row)
            for index in actionIndexes {
                if let item = sender.item(atRow: index) as? FileItem {
                    NSWorkspace.shared.open(URL(filePath: item.path))
                }
            }
        }
    }
}

private class TextFieldCellView: NSTableCellView {
    init(value: String, editable: Bool = false, image: NSImage? = nil, color: NSColor? = nil, tabularNums: Bool = false) {
        super.init(frame: .zero)

        let stack = NSStackView(frame: .zero)
        stack.orientation = .horizontal
        stack.alignment = .centerY
        stack.spacing = 4

        if let image {
            let imageView = NSImageView()
            imageView.translatesAutoresizingMaskIntoConstraints = false
            imageView.image = image
            imageView.imageScaling = .scaleProportionallyDown
            imageView.widthAnchor.constraint(equalToConstant: 16).isActive = true
            imageView.heightAnchor.constraint(equalToConstant: 16).isActive = true
            stack.addView(imageView, in: .leading)
            self.imageView = imageView
        }

        let textField = NSTextField(labelWithString: value)
        textField.isEditable = editable
        if let color {
            textField.textColor = color
        }
        textField.lineBreakMode = .byTruncatingTail
        if tabularNums {
            textField.font = NSFont.monospacedDigitSystemFont(ofSize: NSFont.systemFontSize, weight: .regular)
        }
        stack.addView(textField, in: .leading)
        self.textField = textField

        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)

        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }
}

private func compareWithDesc<T: Comparable>(
    lhs: T, rhs: T, lhsName: String, rhsName: String, desc: AKSortDescriptor
) -> Bool {
    if lhs == rhs {
        return desc.compare(lhsName, rhsName)
    }

    return desc.compare(lhs, rhs)
}

extension [FileItem] {
    fileprivate mutating func sort(desc: AKSortDescriptor) {
        // these are structs so we need to mutate in-place
        for index in self.indices {
            self[index].children?.sort(desc: desc)
        }

        switch desc.columnId {
        case Columns.modified:
            self.sort {
                compareWithDesc(
                    lhs: $0.modified, rhs: $1.modified, lhsName: $0.path, rhsName: $1.path,
                    desc: desc)
            }
        case Columns.size:
            self.sort {
                compareWithDesc(
                    lhs: $0.size, rhs: $1.size, lhsName: $0.path, rhsName: $1.path, desc: desc)
            }
        case Columns.type:
            self.sort {
                compareWithDesc(
                    lhs: $0.type, rhs: $1.type, lhsName: $0.path, rhsName: $1.path, desc: desc)
            }
        default:
            self.sort { desc.compare($0.name, $1.name) }
        }
    }
}

private class FileItem: Identifiable, AKListItem, Equatable, Hashable {
    let path: String
    let name: String
    let type: FileItemType
    let size: Int64
    let modified: Date
    let icon: NSImage
    var children: [FileItem]?

    init(
        path: String, name: String, type: FileItemType, size: Int64, modified: Date, icon: NSImage,
        children: [FileItem]?
    ) {
        self.path = path
        self.name = name
        self.type = type
        self.size = size
        self.modified = modified
        self.icon = icon
        self.children = children
    }

    static func == (lhs: FileItem, rhs: FileItem) -> Bool {
        lhs.path == rhs.path && lhs.name == rhs.name && lhs.type == rhs.type && lhs.size == rhs.size
            && lhs.modified == rhs.modified && lhs.icon == rhs.icon && lhs.children == rhs.children
    }

    func hash(into hasher: inout Hasher) {
        hasher.combine(path)
        hasher.combine(name)
        hasher.combine(type)
        hasher.combine(size)
        hasher.combine(modified)
        hasher.combine(icon)
        hasher.combine(children)
    }

    var listChildren: [any AKListItem]? {
        children
    }

    var textLabel: String? { name }
    var id: String { path }
}

private enum Columns {
    static let name = "name"
    static let modified = "modified"
    static let size = "size"
    static let type = "type"
}

private enum FileItemType: Comparable {
    case regular
    case block
    case character
    case fifo
    case directory
    case symlink
    case socket
    case whiteout
    case unknown

    init(from fileType: FileType) {
        switch fileType {
        case .regular: self = .regular
        case .directory: self = .directory
        case .symlink: self = .symlink
        case .block: self = .block
        case .character: self = .character
        case .fifo: self = .fifo
        case .socket: self = .socket
        case .whiteout: self = .whiteout
        default: self = .unknown
        }
    }

    var description: String {
        switch self {
        case .regular: return "File"
        case .block: return "Block device"
        case .character: return "Character device"
        case .fifo: return "FIFO"
        case .directory: return "Folder"
        case .symlink: return "Symlink"
        case .socket: return "Socket"
        case .whiteout: return "Whiteout"
        case .unknown: return "Unknown"
        }
    }
}

extension FileInfo.Timespec {
    fileprivate var date: Date {
        Date(
            timeIntervalSince1970: TimeInterval(seconds) + TimeInterval(nanoseconds) / 1_000_000_000
        )
    }
}
