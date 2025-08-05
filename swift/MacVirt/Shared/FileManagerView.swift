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
    @EnvironmentObject var toaster: Toaster

    @StateObject private var model = FileManagerOutlineDelegate()

    let rootPath: String
    let readOnly: Bool

    init(rootPath: String, readOnly: Bool = false) {
        self.rootPath = rootPath
        self.readOnly = readOnly
    }

    var body: some View {
        FileManagerNSView(delegate: model)
            .onChange(of: rootPath, initial: true) { _, newRootPath in
                Task {
                    do {
                        try await model.loadRoot(rootPath: newRootPath, readOnly: readOnly)
                    } catch {
                        model.toaster.error(title: "Error loading files", error: error)
                    }
                }
            }
    }
}

private struct FileManagerNSView: NSViewRepresentable {
    @EnvironmentObject var toaster: Toaster

    let delegate: FileManagerOutlineDelegate

    func makeNSView(context: Context) -> NSScrollView {
        let view = FileManagerOutlineView()

        let treeController = NSTreeController()
        treeController.leafKeyPath = "isLeaf"
        treeController.childrenKeyPath = "children"
        treeController.objectClass = FileItem.self
        treeController.bind(NSBindingName.contentArray, to: delegate, withKeyPath: "rootItems")

        delegate.view = view
        delegate.treeController = treeController // weak
        delegate.toaster = toaster
        view.delegate = delegate  // weak

        view.bind(NSBindingName.content, to: treeController, withKeyPath: "arrangedObjects")
        view.bind(NSBindingName.selectionIndexPaths, to: treeController, withKeyPath: "selectionIndexPaths")
        view.bind(NSBindingName.sortDescriptors, to: treeController, withKeyPath: "sortDescriptors")

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
        // done in NSTextField instead
        colName.isEditable = false
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
    private var filesDelegate: FileManagerOutlineDelegate {
        delegate as! FileManagerOutlineDelegate
    }

    var quickLookOpen = false

    // open quick look on space key pressed
    override func keyDown(with event: NSEvent) {
        if event.charactersIgnoringModifiers == " " {
            if quickLookOpen {
                QLPreviewPanel.shared()?.close()
            } else {
                QLPreviewPanel.shared()?.makeKeyAndOrderFront(nil)
            }
        } else {
            // Pass other key events to the outline view for navigation, selection, etc.
            super.keyDown(with: event)
        }
    }

    // nsresponder commands
    @objc func copy(_ sender: Any?) {
        if !filesDelegate.readOnly {
            filesDelegate.copyAction(actionIndexes: selectedRowIndexes)
        }
    }

    @objc func paste(_ sender: Any?) {
        if !filesDelegate.readOnly {
            filesDelegate.pasteAction(actionIndexes: selectedRowIndexes)
        }
    }

    func numberOfPreviewItems(in panel: QLPreviewPanel!) -> Int {
        // number of selected items
        return max(self.selectedRowIndexes.count, 1)
    }

    func previewPanel(_ panel: QLPreviewPanel!, previewItemAt index: Int) -> QLPreviewItem! {
        if self.selectedRowIndexes.isEmpty, let rootPath = filesDelegate.rootPath {
            return FileManagerPreviewItem(treeNode: nil, filePath: rootPath, icon: NSWorkspace.shared.icon(forFile: rootPath))
        } else {
            let viewIndex = Array(self.selectedRowIndexes)[index]
            let node = self.item(atRow: viewIndex) as! NSTreeNode
            let item = node.representedObject as! FileItem
            return FileManagerPreviewItem(treeNode: node, filePath: item.path, icon: item.icon)
        }
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
        let row = self.row(forItem: item.treeNode)
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

        return item.icon
    }

    override func acceptsPreviewPanelControl(_ panel: QLPreviewPanel!) -> Bool {
        return true
    }

    override func beginPreviewPanelControl(_ panel: QLPreviewPanel!) {
        panel.dataSource = self
        panel.delegate = self
        self.quickLookOpen = true
    }

    override func endPreviewPanelControl(_ panel: QLPreviewPanel!) {
        panel.dataSource = nil
        panel.delegate = nil
        self.quickLookOpen = false
    }

    override func menu(for event: NSEvent) -> NSMenu? {
        // trigger border highlight
        super.menu(for: event)

        let actionIndexes: IndexSet = if clickedRow == -1 {
            []
        } else if selectedRowIndexes.contains(clickedRow) {
            selectedRowIndexes
        } else {
            [clickedRow]
        }
        return RIMenu {
            if actionIndexes.count >= 1 {
                RIMenuItem("Open") { [self] in
                    for index in actionIndexes {
                        if let node = item(atRow: index) as? NSTreeNode,
                            let item = node.representedObject as? FileItem
                        {
                            NSWorkspace.shared.open(URL(filePath: item.path))
                        }
                    }
                }

                RIMenuItem.separator()

                if !filesDelegate.readOnly {
                    RIMenuItem("Rename") {
                        // TODO
                    }

                    RIMenuItem("Delete") {
                        // TODO
                    }

                    RIMenuItem.separator()

                    RIMenuItem("Copy") {
                        self.filesDelegate.copyAction(actionIndexes: actionIndexes)
                    }
                }
            }

            RIMenuItem.separator()

            RIMenuItem("Show in Finder") { [self] in
                if actionIndexes.isEmpty, let rootPath = filesDelegate.rootPath {
                    NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: rootPath)
                } else {
                    for index in actionIndexes {
                        if let node = item(atRow: index) as? NSTreeNode,
                            let item = node.representedObject as? FileItem
                        {
                            NSWorkspace.shared.selectFile(
                                item.path, inFileViewerRootedAtPath: item.path)
                        }
                    }
                }
            }

            RIMenuItem("Quick Look") {
                QLPreviewPanel.shared()?.makeKeyAndOrderFront(nil)
            }

            RIMenuItem.separator()

            if !filesDelegate.readOnly {
                if actionIndexes.count == 0 {
                    RIMenuItem("New Folder") {
                        // TODO
                    }
                }

                if actionIndexes.count <= 1 {
                    RIMenuItem("Paste") {
                        self.filesDelegate.pasteAction(actionIndexes: actionIndexes)
                    }
                }
            }

            RIMenuItem.separator()
        }.menu
    }

    @objc func openInFinderItem() {
        // openInFinderItem is only called if a single item is selected
        let node = item(atRow: selectedRowIndexes.first!) as! NSTreeNode
        let item = node.representedObject as! FileItem
        NSWorkspace.shared.open(URL(filePath: item.path))
    }
}

private class FileManagerPreviewItem: NSObject, QLPreviewItem {
    let treeNode: NSTreeNode?
    let icon: NSImage
    var previewItemURL: URL!

    init(treeNode: NSTreeNode?, filePath: String, icon: NSImage) {
        self.treeNode = treeNode
        self.icon = icon
        super.init()
        previewItemURL = URL(filePath: filePath)
    }
}

private class FileManagerOutlineDelegate: NSObject, NSOutlineViewDelegate,
    ObservableObject
{
    var toaster: Toaster!
    var view: FileManagerOutlineView!
    weak var treeController: NSTreeController?

    // per-root state
    var readOnly = false
    var rootPath: String?
    private var expandedPaths = Set<String>()
    private var fsEvents: FSEventsListener?
    @objc dynamic var rootItems: [FileItem] = []

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
                    children: nil)
                if file.type == .directory && expandedPaths.contains(path) {
                    item.children = try await getDirectoryItems(path: path)
                }

                items.append(item)
            }
        }

        return items
    }

    @MainActor
    func loadRoot(rootPath: String, readOnly: Bool) async throws {
        // stop old fs events
        fsEvents = nil
        expandedPaths.removeAll()

        self.rootPath = rootPath
        self.readOnly = readOnly
        self.rootItems = try await getDirectoryItems(path: rootPath)

        // start fs events
        do {
            fsEvents = try FSEventsListener(paths: [rootPath], flags: UInt32(kFSEventStreamCreateFlagNoDefer), latency: 0.1, callback: { events in
                Task { @MainActor in
                    // TODO: granular reload
                    for event in events {
                        print("fs event: \(event)")
                    }
                    self.rootItems = try await self.getDirectoryItems(path: rootPath)
                }
            })
        } catch {
            NSLog("Error starting fs events: \(error)")
        }

        // clear selection
        treeController?.setSelectionIndexPaths([])
    }

    @MainActor
    func expandItem(item: FileItem) async throws {
        expandedPaths.insert(item.path)
        item.children = try await getDirectoryItems(path: item.path)
    }

    func collapseItem(item: FileItem) {
        expandedPaths.remove(item.path)
        item.children = nil
    }

    func copyAction(actionIndexes: IndexSet) {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        for index in actionIndexes {
            if let node = view.item(atRow: index) as? NSTreeNode,
                let item = node.representedObject as? FileItem
            {
                pasteboard.writeObjects([URL(filePath: item.path) as NSURL])
            }
        }
    }

    func pasteAction(actionIndexes: IndexSet) {
        var destinationPath: URL
        if let index = actionIndexes.first {
            let node = view.item(atRow: index) as! NSTreeNode
            let selectedFile = node.representedObject as! FileItem
            destinationPath = URL(filePath: selectedFile.path)
            if selectedFile.type != .directory {
                destinationPath = destinationPath.deletingLastPathComponent()
            }
        } else if view.selectedRowIndexes.count == 0, let rootPath {
            destinationPath = URL(filePath: rootPath)
        } else {
            return
        }

        let pasteboard = NSPasteboard.general
        guard let items = pasteboard.readObjects(forClasses: [NSURL.self], options: nil) as? [NSURL]
        else { return }
        Task {
            for item in items {
                do {
                    if let path = item.path, let lastPathComponent = item.lastPathComponent {
                        try await FileSystem.shared.copyItem(at: FilePath(path), to: FilePath(destinationPath.appendingPathComponent(lastPathComponent).path))
                    }
                } catch {
                    toaster.error(title: "Error copying item", message: error.localizedDescription)
                }
            }
        }
    }

    // MARK: - delegat
    func outlineView(_ outlineView: NSOutlineView, viewFor tableColumn: NSTableColumn?, item: Any)
        -> NSView?
    {
        let node = item as! NSTreeNode
        let item = node.representedObject as! FileItem
        switch tableColumn?.identifier.rawValue {
        case Columns.name:
            return TextFieldCellView(value: item.name, editable: !readOnly, image: item.icon, onRename: { newName in
                var newPath = FilePath(item.path)
                newPath.removeLastComponent()
                newPath.append(newName)

                do {
                    try await FileSystem.shared.moveItem(at: FilePath(item.path), to: newPath)
                } catch {
                    self.toaster.error(title: "Error renaming item", error: error)
                }
            })
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

    func outlineViewSelectionDidChange(_ notification: Notification) {
        if view.quickLookOpen {
            QLPreviewPanel.shared()?.reloadData()
        }
    }

    //    func outlineView(_ outlineView: NSOutlineView, pasteboardWriterForItem item: Any) -> NSPasteboardWriting? {
    //    }

    func outlineViewItemDidExpand(_ notification: Notification) {
        if let node = notification.userInfo?["NSObject"] as? NSTreeNode,
            let item = node.representedObject as? FileItem,
            item.children == nil
        {
            Task { @MainActor in
                print("expanding \(item.name)")
                do {
                    try await expandItem(item: item)
                } catch {
                    self.toaster.error(title: "Error expanding item", error: error)
                }
            }
        }
    }

    func outlineViewItemDidCollapse(_ notification: Notification) {
        if let node = notification.userInfo?["NSObject"] as? NSTreeNode,
            let item = node.representedObject as? FileItem,
            item.children != nil
        {
            print("collapsing \(item.name)")
            collapseItem(item: item)
        }
    }

    @objc func onDoubleClick(_ sender: NSOutlineView) {
        // expand or collapse row
        let row = sender.clickedRow
        guard row != -1 else {
            return
        }

        let node = sender.item(atRow: row) as? NSTreeNode
        guard let item = node?.representedObject as? FileItem else { return }
        if item.type == .directory {
            if sender.isItemExpanded(item) {
                sender.animator().collapseItem(item)
            } else {
                sender.animator().expandItem(item)
            }
        } else {
            let actionIndexes = sender.selectedRowIndexes.contains(row) ? sender.selectedRowIndexes : IndexSet(integer: row)
            for index in actionIndexes {
                if let node = sender.item(atRow: index) as? NSTreeNode,
                    let item = node.representedObject as? FileItem
                {
                    NSWorkspace.shared.open(URL(filePath: item.path))
                }
            }
        }
    }
}

private class TextFieldCellView: NSTableCellView, NSTextFieldDelegate {
    var renameCallback: ((String) async throws -> Void)?
    var toaster: Toaster!

    init(value: String, editable: Bool = false, image: NSImage? = nil, color: NSColor? = nil, tabularNums: Bool = false, onRename renameCallback: ((String) async -> Void)? = nil) {
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
        textField.delegate = self
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

        if let renameCallback {
            self.renameCallback = renameCallback
        }
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    func control(_ control: NSControl, textShouldEndEditing fieldEditor: NSText) -> Bool {
        let newName = fieldEditor.string
        Task {
            do {
                try await renameCallback?(newName)
            } catch {
                self.toaster.error(title: "Error renaming item", error: error)
            }
        }
        return true
    }
}

private class FileItem: NSObject, Identifiable {
    let path: String
    @objc dynamic let name: String
    @objc dynamic let type: FileItemType
    @objc dynamic let size: Int64
    @objc dynamic let modified: Date
    let icon: NSImage
    @objc dynamic var children: [FileItem]?

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

    var id: String { path }
    @objc dynamic var isLeaf: Bool { type != .directory }
}

private enum Columns {
    static let name = "name"
    static let modified = "modified"
    static let size = "size"
    static let type = "type"
}

@objc private enum FileItemType: Int {
    case regular = 0
    case block = 1
    case character = 2
    case fifo = 3
    case directory = 4
    case symlink = 5
    case socket = 6
    case whiteout = 7
    case unknown = 8

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
