import SwiftUI
import Defaults
import _NIOFileSystem

struct DockerContainerFilesTab: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @StateObject private var model = FilesViewModel()
    @State private var sort = AKSortDescriptor(columnId: Columns.name, ascending: true)
    @State private var selection: Set<String> = []

    let container: DKContainer

    var body: some View {
        //autosaveName: Defaults.Keys.dockerContainerFiles_autosaveOutline
        AKList(AKSection.single(model.items), selection: $selection, sort: $sort, rowHeight: 20, flat: false, onExpand: { item in
            Task {
                do {
                    try await model.expandItem(item: item)
                } catch {
                    NSLog("Error expanding item: \(error)")
                }
            }
        }, onCollapse: { item in
            model.collapseItem(item: item)
        }, columns: [
            akColumn(id: Columns.name, title: "Name", width: 250, alignment: .left) { item in
                HStack(spacing: 4) {
                    Image(nsImage: item.icon)
                        .resizable()
                        .frame(width: 16, height: 16)

                    Text(item.name)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .akListContextMenu {
                    Button {
                        NSWorkspace.shared.open(URL(fileURLWithPath: item.path))
                    } label: {
                        Label("Open", systemImage: "arrow.up.right.square")
                    }

                    Button {
                        NSWorkspace.shared.selectFile(item.path, inFileViewerRootedAtPath: item.path)
                    } label: {
                        Label("Show in Finder", systemImage: "folder")
                    }

                    Divider()

                    Button {
                        Task {
                            do {
                                try await FileSystem.shared.removeItem(at: FilePath(item.path), recursively: true)
                        } catch {
                                NSLog("Error removing file: \(error)")
                            }
                        }
                    } label: {
                        Label("Delete", systemImage: "trash")
                    }
                }
                .akListOnDoubleClick {
                    if item.type != .directory {
                        NSWorkspace.shared.open(URL(fileURLWithPath: item.path))
                    }
                }
            },
            akColumn(id: Columns.modified, title: "Date Modified", width: 175, alignment: .left) { item in
                Text(item.modified.formatted(date: .abbreviated, time: .shortened))
                    .foregroundColor(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
            },
            akColumn(id: Columns.size, title: "Size", width: 72, alignment: .right) { item in
                if item.type == .regular {
                    Text(item.size.formatted(.byteCount(style: .file)))
                        .foregroundColor(.secondary)
                        .frame(maxWidth: .infinity, alignment: .trailing)
                }
            },
            akColumn(id: Columns.type, title: "Kind", width: 100, alignment: .left) { item in
                Text(item.type.description)
                    .foregroundColor(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        ])
        .onReceive(vmModel.toolbarActionRouter) { action in
            if action == .dockerOpenContainerInNewWindow {
                container.openFolder()
            }
        }
        .onChange(of: container, initial: true) { _, newContainer in
            Task {
                do {
                    try await model.loadRoot(container: newContainer)
                } catch {
                    NSLog("Error loading files: \(error)")
                }
            }
        }
        .onChange(of: sort) { _, newSort in
            model.sortDesc = newSort
            model.reSort()
        }
    }
}

private class FilesViewModel: ObservableObject {
    var sortDesc = AKSortDescriptor(columnId: Columns.name, ascending: true)

    private var expandedPaths = Set<String>()
    @Published var items: [FileItem] = []

    private func getDirectoryItems(path: String) async throws -> [FileItem] {
        var items = [FileItem]()
        try await FileSystem.shared.withDirectoryHandle(atPath: FilePath(path)) { dir in
            for try await file in dir.listContents() {
                guard let info = try await FileSystem.shared.info(forFileAt: file.path, infoAboutSymbolicLink: true) else {
                    continue
                }

                let path = file.path.string
                let icon = NSWorkspace.shared.icon(forFile: path)
                let item = FileItem(path: path, name: file.name.string, type: FileItemType(from: file.type), size: info.size, modified: info.lastDataModificationTime.date, icon: icon, children: file.type == .directory ? [] : nil)
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
    func loadRoot(container: DKContainer) async throws {
        expandedPaths.removeAll()
        self.items = try await getDirectoryItems(path: container.nfsPath)
    }

    @MainActor
    func expandItem(item: FileItem) async throws {
        expandedPaths.insert(item.path)
        item.children = try await getDirectoryItems(path: item.path)
        self.items = items.map { FileItem(path: $0.path, name: $0.name, type: $0.type, size: $0.size + 1, modified: $0.modified, icon: $0.icon, children: $0.children) }
        print("expanded item \(item.path) children=\(item.children)")
    }

    @MainActor
    func collapseItem(item: FileItem) {
        expandedPaths.remove(item.path)
    }

    func reSort() {
        items.sort(desc: sortDesc)
    }
}

private func compareWithDesc<T: Comparable>(lhs: T, rhs: T, lhsName: String, rhsName: String, desc: AKSortDescriptor) -> Bool {
    if lhs == rhs {
        return desc.compare(lhsName, rhsName)
    }

    return desc.compare(lhs, rhs)
}

private extension [FileItem] {
    mutating func sort(desc: AKSortDescriptor) {
        // these are structs so we need to mutate in-place
        for index in self.indices {
            self[index].children?.sort(desc: desc)
        }

        switch desc.columnId {
        case Columns.modified:
            self.sort { compareWithDesc(lhs: $0.modified, rhs: $1.modified, lhsName: $0.path, rhsName: $1.path, desc: desc) }
        case Columns.size:
            self.sort { compareWithDesc(lhs: $0.size, rhs: $1.size, lhsName: $0.path, rhsName: $1.path, desc: desc) }
        case Columns.type:
            self.sort { compareWithDesc(lhs: $0.type, rhs: $1.type, lhsName: $0.path, rhsName: $1.path, desc: desc) }
        default:
            self.sort { desc.compare($0.name, $1.name) }
        }
    }
}

private class FileItem: Identifiable, AKListItem, Equatable {
    let path: String
    let name: String
    let type: FileItemType
    let size: Int64
    let modified: Date
    let icon: NSImage
    var children: [FileItem]?

    init(path: String, name: String, type: FileItemType, size: Int64, modified: Date, icon: NSImage, children: [FileItem]?) {
        self.path = path
        self.name = name
        self.type = type
        self.size = size
        self.modified = modified
        self.icon = icon
        self.children = children
    }

    static func == (lhs: FileItem, rhs: FileItem) -> Bool {
        lhs.path == rhs.path && lhs.name == rhs.name && lhs.type == rhs.type && lhs.size == rhs.size && lhs.modified == rhs.modified && lhs.icon == rhs.icon && lhs.children == rhs.children
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
        case .symlink: return "Symbolic link"
        case .socket: return "Socket"
        case .whiteout: return "Whiteout"
        case .unknown: return "Unknown"
        }
    }
}

private extension FileInfo.Timespec {
    var date: Date {
        Date(timeIntervalSince1970: TimeInterval(seconds) + TimeInterval(nanoseconds) / 1_000_000_000)
    }
}
