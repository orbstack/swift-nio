//
// Created by Danny Lin on 5/7/23.
//

import Combine
import Defaults
import Foundation
import SwiftUI
import NIOCore
import os
import Carbon
import SwiftUIIntrospect

private let inspectorHeight: CGFloat = 120

let logsMaxLines = 5000
private let maxChars = logsMaxLines * 150  // avg line len - easier to do it like this
private let bottomScrollThreshold: CGFloat = 30
private let fontSize = 12.5

private let terminalFont = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .regular)
private let terminalFontBold = NSFont.monospacedSystemFont(ofSize: fontSize, weight: .bold)
private let terminalColor = NSColor.textColor

private let urlRegex = try! NSRegularExpression(
    pattern:
        #"http(s)?:\/\/(www\.)?[-a-zA-Z0-9@:%._\+~#=]{2,256}(\.[a-z]{2,6})?\b([-a-zA-Z0-9@:%_\+.~#?&\/\/=]*)"#
)
private let ansiColorRegex = try! NSRegularExpression(pattern: #"\u001B\[([0-9]{1,2};?)*?m"#)
private let unsupportedAnsiRegex = try! NSRegularExpression(
    pattern: #"\u001B\[(?:[=?].+[a-zA-Z]|\d+[a-zA-Z])"#)

private let ansiColorPalette: [NSColor] = [
    // keep in mind that ansi colors are meant for white-on-black
    .textBackgroundColor,  // black
    .systemRed,
    .systemGreen,
    .systemOrange,  // systemYellow has bad contrast in light
    .systemBlue,
    .systemPurple,
    .systemCyan,
    .textColor,  // white

    // bright colors
    .systemGray,  // "bright black" is used for dim text: echo -e '\e[90m2023-09-01T00:48:52.163\e[0m Starting'
    // TODO: blend 0.4 with textColor, for light and dark
    .systemRed,
    .systemGreen,
    .systemOrange,
    .systemBlue,
    .systemPurple,
    .systemCyan,
    .textColor,  // white
]

private struct AnsiState: Equatable {
    var bold = false
    var underline = false
    var colorFg: Int?
    var colorBg: Int?

    func addAttribute(to: NSMutableAttributedString, range: NSRange) {
        if bold {
            to.addAttribute(.font, value: terminalFontBold, range: range)
        }
        if underline {
            to.addAttribute(.underlineStyle, value: NSUnderlineStyle.single.rawValue, range: range)
        }
        if let colorFg {
            to.addAttribute(.foregroundColor, value: ansiColorPalette[colorFg], range: range)
        }
        if let colorBg {
            to.addAttribute(.backgroundColor, value: ansiColorPalette[colorBg], range: range)
        }
    }
}

private class PtyPipe: Pipe, @unchecked Sendable {
    private let _fileHandleForReading: FileHandle
    private let _fileHandleForWriting: FileHandle

    override init() {
        let masterFd = posix_openpt(O_CLOEXEC)
        if masterFd == -1 {
            // fallback = pipe
            let pipe = Pipe()
            _fileHandleForReading = pipe.fileHandleForReading
            _fileHandleForWriting = pipe.fileHandleForWriting
            return
        }
        grantpt(masterFd)
        unlockpt(masterFd)

        let slaveFd = open(ptsname(masterFd), O_RDWR | O_NOCTTY | O_CLOEXEC)
        guard slaveFd != -1 else {
            close(masterFd)
            // fallback = pipe
            let pipe = Pipe()
            _fileHandleForReading = pipe.fileHandleForReading
            _fileHandleForWriting = pipe.fileHandleForWriting
            return
        }

        // use FileHandle immediately
        _fileHandleForReading = FileHandle(fileDescriptor: masterFd, closeOnDealloc: true)
        _fileHandleForWriting = FileHandle(fileDescriptor: slaveFd, closeOnDealloc: true)
    }

    override var fileHandleForReading: FileHandle {
        _fileHandleForReading
    }

    override var fileHandleForWriting: FileHandle {
        _fileHandleForWriting
    }
}

private class AsyncPipeReader {
    private var buf = Data(capacity: 1024)
    private var lastCh: UInt8 = 0

    private let pipe: Pipe
    private let callback: (String) -> Void

    init(pipe: Pipe, callback: @escaping (String) -> Void) {
        self.pipe = pipe
        self.callback = callback
        pipe.fileHandleForReading.readabilityHandler = onReadable
    }

    private func onReadable(handle: FileHandle) {
        for ch in handle.availableData {
            // \r for pty logs
            if ch == 10 || ch == 13 {  // \n or \r
                if lastCh == 13 {
                    // skip \n after \r
                    lastCh = ch
                    continue
                }

                // do not append \n
                callback(String(decoding: buf, as: UTF8.self))
                buf.removeAll(keepingCapacity: true)
            } else {
                buf.append(ch)
            }

            lastCh = ch
        }
    }

    func finish() {
        pipe.fileHandleForReading.readabilityHandler = nil
        // drain
        onReadable(handle: pipe.fileHandleForReading)
    }
}

class CommandViewModel: ObservableObject {
    let clearCommand = PassthroughSubject<Void, Never>()
    let copyAllCommand = PassthroughSubject<Void, Never>()

    @Published var searchField = ""
}

private struct LogLine {
    let seq: UInt64
    let text: NSAttributedString
}

private struct RawLogContents {
    var lines = CircularBuffer<LogLine>()
    var seq: UInt64 = 0
}

private struct DisplayLogContents {
    var lines = CircularBuffer<LogLine>()
    var searchFilter = ""

    func shouldShowLine(_ line: LogLine) -> Bool {
        if searchFilter.isEmpty {
            return true
        }

        return line.text.string.contains(searchFilter)
    }
}

class LogsViewModel: ObservableObject {
    fileprivate var displayContents = OSAllocatedUnfairLock(initialState: DisplayLogContents())
    fileprivate var rawContents = OSAllocatedUnfairLock(initialState: RawLogContents())
    fileprivate let updateEvent = PassthroughSubject<Void, Never>()

    var process: Process?
    private var lastAnsiState = AnsiState()
    private var isFirstStart = true

    private var lastCmdExe: String?
    private var lastArgs: [String]?
    private var lastLineDate: Date?

    private var cancellables: Set<AnyCancellable> = []
    @Published var lastContainerName: String?  // saved once we get id

    @Published var selectedLineText = ""
    var selectedSeqs = Set<UInt64>()

    @MainActor
    func monitorContainers(vmModel: VmViewModel, cid: DockerContainerId) {
        vmModel.$dockerContainers.sink { [weak self] containers in
            guard let self else { return }

            // if containers list changes,
            // and process has exited,
            // and (container ID && it's running) or (containerName && it's running) or (composeProject && any running)
            if self.process != nil {
                return
            }
            guard let containers else {
                return
            }

            if case let .container(containerId) = cid,
                let container = containers.byId[containerId],
                container.running
            {
                self.restart()
            } else if let lastContainerName,
                let container = containers.byName[lastContainerName],
                container.running
            {
                self.restart()
            } else if case let .compose(composeProject) = cid,
                let children = containers.byComposeProject[composeProject],
                children.contains(where: { $0.running })
            {
                self.restart()
            }
        }.store(in: &cancellables)
    }

    @MainActor
    func monitorCommands(commandModel: CommandViewModel) {
        commandModel.clearCommand.sink { [weak self] in
            self?.clear()
        }.store(in: &cancellables)

        commandModel.copyAllCommand.sink { [weak self] in
            self?.copyAll()
        }.store(in: &cancellables)

        // search command is monitored by GUI
    }

    @MainActor
    func start(cmdExe: String, args: [String], clearAndRestart: Bool = false) {
        // reset first
        stop()

        NSLog("Starting log stream: cmdExe=\(cmdExe), args=\(args)")
        lastAnsiState = AnsiState()
        lastCmdExe = cmdExe
        lastArgs = args
        let args = args
        if clearAndRestart {
            // clear for compose checkbox disabledChildren change
            clear()
        } else {
            // append arg to filter since last received line, for restart
            // we don't do fancy restart continuation logic anymore:
            // it's not necessary, as the CLI will replay a reasonable amount of logs
            // this causes a lot of issues that aren't worth fixing
            /*
            if let lastLineDate {
                let formatter = ISO8601DateFormatter()
                formatter.formatOptions.insert(.withFractionalSeconds)
                // for k8s this is --since-time
                if cmdExe == AppConfig.kubectlExe {
                    args.append("--since-time=\(formatter.string(from: lastLineDate))")
                } else {
                    args.append("--since=\(formatter.string(from: lastLineDate))")
                }
            }
            */

            // always clear
            clear()

            // if not first start, add delimiter
            // if !isFirstStart {
            //     addDelimiter()
            // }
        }

        isFirstStart = false

        let task = Process()
        task.launchPath = cmdExe
        // force: we do existing-data check in GUI
        task.arguments = args

        // env is more robust, user can mess with context
        var newEnv = ProcessInfo.processInfo.environment
        newEnv["TERM"] = "xterm"  // 16 color only
        newEnv["DOCKER_HOST"] = "unix://\(Files.dockerSocket)"
        task.environment = newEnv

        // use pty to make docker-compose print colored prefixes
        let pipe = PtyPipe()
        task.standardOutput = pipe
        task.standardError = pipe
        // AsyncBytes is not actually async, it blocks on read and occupies a task thread
        // so can't run multiple tasks concurrently
        let reader = AsyncPipeReader(pipe: pipe) { [weak self] line in
            guard let self else { return }
            self.lastLineDate = Date()  // for restart
            self.add(terminalLine: line)
        }

        task.terminationHandler = { process in
            let status = process.terminationStatus
            let reason = process.terminationReason

            // mark as exited for restarting on container state change
            if self.process?.processIdentifier == process.processIdentifier {
                self.process = nil
            }

            DispatchQueue.main.async { [weak self] in
                guard let self else { return }

                reader.finish()
                // ignore our own SIGKILL
                if status != 0 && reason != .uncaughtSignal {
                    add(error: "Failed with status \(status)")
                }
            }
        }
        process = task

        do {
            try task.run()
        } catch {
            add(error: "Failed to start log stream: \(error)")
        }
    }

    func stop() {
        NSLog("Ending log stream: cmdExe=\(lastCmdExe ?? ""), args=\(lastArgs ?? [])")

        if let process {
            // .terminate sends SIGTERM
            kill(process.processIdentifier, SIGKILL)
        }
        process = nil

        // don't restart
        lastCmdExe = nil
        lastArgs = nil
    }

    @MainActor
    func restart() {
        NSLog("Restarting log stream: cmdExe=\(lastCmdExe ?? ""), args=\(lastArgs ?? [])")
        if let lastCmdExe, let lastArgs {
            start(cmdExe: lastCmdExe, args: lastArgs)
        }
    }

    private func add(terminalLine: String) {
        let attributedStr = NSMutableAttributedString(string: terminalLine)
        // font
        attributedStr.addAttribute(
            .font, value: terminalFont, range: NSRange(location: 0, length: attributedStr.length))
        // color
        attributedStr.addAttribute(
            .foregroundColor, value: terminalColor,
            range: NSRange(location: 0, length: attributedStr.length))

        // parse links first, before indexes change
        var matches = urlRegex.matches(
            in: terminalLine, range: NSRange(location: 0, length: terminalLine.utf16.count))
        for match in matches {
            let url = (terminalLine as NSString).substring(with: match.range)
            attributedStr.addAttribute(.link, value: url, range: match.range)
        }

        // parse colors from ANSI escapes - state machine
        matches = ansiColorRegex.matches(
            in: terminalLine, range: NSRange(location: 0, length: terminalLine.utf16.count))
        var state = AnsiState()
        var lastI = 0
        for match in matches {
            // ranges aren't repeated properly, so do it ourselves
            let codes = (terminalLine as NSString).substring(with: match.range)
                .replacingOccurrences(of: "\u{001B}[", with: "")
                .replacingOccurrences(of: "m", with: "")
                .split(separator: ";")
            // empty ESC[m = reset
            if codes.isEmpty {
                state = AnsiState()
            }

            for codeStr in codes {
                let code = Int(codeStr)
                guard let code else {
                    // failed to parse
                    continue
                }

                switch code {
                case 0:
                    // reset
                    state = AnsiState()
                case 1:
                    state.bold = true
                case 4:
                    state.underline = true
                case 30...37:
                    state.colorFg = code - 30
                case 39:
                    state.colorFg = nil
                case 40...47:
                    state.colorBg = code - 40
                case 49:
                    state.colorBg = nil
                // bright = bold + color
                case 90...97:
                    state.colorFg = 8 + code - 90
                    state.bold = true
                case 100...107:
                    state.colorBg = 8 + code - 100
                    state.bold = true
                default:
                    continue
                }
            }

            // state updated. add last mark
            if state != lastAnsiState {
                lastAnsiState.addAttribute(
                    to: attributedStr,
                    range: NSRange(location: lastI, length: match.range.location - lastI))
                lastAnsiState = state
                lastI = match.range.location
            }
        }
        // add terminating mark
        state.addAttribute(
            to: attributedStr,
            range: NSRange(location: lastI, length: terminalLine.utf16.count - lastI))
        lastAnsiState = state
        // then delete escapes
        for match in matches.reversed() {
            attributedStr.deleteCharacters(in: match.range)
        }

        // delete unsupported escapes (like ESC[?25l used by nextjs, and ESC[4D [move cursor by columns])
        let newStr = attributedStr.string
        matches = unsupportedAnsiRegex.matches(
            in: newStr, range: NSRange(location: 0, length: newStr.utf16.count))
        for match in matches.reversed() {
            attributedStr.deleteCharacters(in: match.range)
        }

        add(attributedString: attributedStr)
    }

    private func add(attributedString: NSMutableAttributedString) {
        let line = rawContents.withLock { contents in
            if contents.lines.count >= logsMaxLines {
                contents.lines.removeFirst()
            }

            let line = LogLine(seq: contents.seq, text: attributedString)
            contents.seq += 1
            contents.lines.append(line)

            return line
        }

        displayContents.withLock { contents in
            if !contents.shouldShowLine(line) {
                return
            }

            if contents.lines.count >= logsMaxLines {
                contents.lines.removeFirst()
            }
            contents.lines.append(line)
        }

        updateEvent.send()
    }

    private func add(error: String) {
        let str = NSMutableAttributedString(string: error + "\n")
        // bold font
        str.addAttribute(
            .font, value: terminalFontBold, range: NSRange(location: 0, length: str.length))
        // red
        str.addAttribute(
            .foregroundColor, value: NSColor.systemRed,
            range: NSRange(location: 0, length: str.length))
        add(attributedString: str)
    }

    private func addDelimiter() {
        let str = NSMutableAttributedString(
            string: "\n─────────────────── restarted ──────────────────\n\n")
        // font (bold causes overlapping box chars)
        str.addAttribute(
            .font, value: terminalFont, range: NSRange(location: 0, length: str.length))
        // secondary gray (secondaryLabelColor also causes overlap bleed)
        str.addAttribute(
            .foregroundColor, value: NSColor.systemGray,
            range: NSRange(location: 0, length: str.length))
        add(attributedString: str)
    }

    func clear() {
        rawContents.withLock { contents in
            contents.lines.removeAll()
        }
        displayContents.withLock { contents in
            contents.lines.removeAll()
        }
        updateEvent.send()
    }

    func copyAll() {
        let str = displayContents.withLock { contents in
            var str = String()
            for line in contents.lines {
                if !str.isEmpty {
                    str.append("\n")
                }
                str.append(line.text.string)
            }
            return str
        }
        NSPasteboard.copy(str)
    }

    func setSearchFilter(_ filter: String) {
        displayContents.withLock { contents in
            contents.searchFilter = filter
            contents.lines.removeAll()

            // TODO: is this deadlock safe?
            rawContents.withLock { rawContents in
                for line in rawContents.lines {
                    if contents.shouldShowLine(line) {
                        contents.lines.append(line)
                    }
                }
            }
        }
        updateEvent.send()
    }
}

private class LogsNSTableView: NSTableView {
    override func keyDown(with event: NSEvent) {
        super.keyDown(with: event)

        if event.keyCode == kVK_Escape {
            // deselect all
            self.selectRowIndexes(IndexSet(), byExtendingSelection: false)
            self.delegate?.tableViewSelectionIsChanging?(Notification(name: .init(""), object: self))
        }
    }

    // add padding without affecting scroller
    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(NSSize(width: newSize.width, height: newSize.height + 12))
    }

    @objc func copy(_ sender: Any?) {
        let delegate = self.delegate as! LogsTextView.Coordinator
        delegate.copySelected(tableView: self)
    }
}

private class LogsTableCellView: NSTableCellView {
    let seq: UInt64

    init(seq: UInt64) {
        self.seq = seq
        super.init(frame: .zero)
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }
}

private struct LogsTextView: NSViewRepresentable {
    let model: LogsViewModel
    let commandModel: CommandViewModel
    let topInset: CGFloat

    class Coordinator: NSObject, NSTableViewDelegate, NSTableViewDataSource {
        let model: LogsViewModel
        var cancellables = Set<AnyCancellable>()

        var lastScrolledSeq: UInt64?

        init(model: LogsViewModel) {
            self.model = model
        }

        func numberOfRows(in tableView: NSTableView) -> Int {
            model.displayContents.withLock { contents in
                contents.lines.count
            }
        }

        func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int)
            -> NSView?
        {
            guard let line = model.displayContents.withLock({ contents in
                if row >= contents.lines.count {
                    return LogLine?(nil)
                }
                return contents.lines[offset: row]
            }) else {
                return nil
            }

            let textView = NSTextField(labelWithAttributedString: line.text)
            textView.translatesAutoresizingMaskIntoConstraints = false
            textView.usesSingleLineMode = true
            textView.lineBreakMode = .byTruncatingTail

            let cellView = LogsTableCellView(seq: line.seq)
            cellView.textField = textView
            cellView.addSubview(textView)
            NSLayoutConstraint.activate([
                textView.leadingAnchor.constraint(equalTo: cellView.leadingAnchor),
                textView.trailingAnchor.constraint(equalTo: cellView.trailingAnchor),
                textView.centerYAnchor.constraint(equalTo: cellView.centerYAnchor),
                textView.heightAnchor.constraint(equalToConstant: 16),
            ])

            return cellView
        }

        func tableViewSelectionIsChanging(_ notification: Notification) {
            let tableView = notification.object as! NSTableView
            var selectedLineText = ""
            let selectedSeqs = tableView.selectedRowIndexes.compactMap { row in
                guard let cellView = tableView.view(atColumn: 0, row: row, makeIfNecessary: false) as? LogsTableCellView else {
                    return UInt64?(nil)
                }
                selectedLineText = cellView.textField!.stringValue
                return cellView.seq
            }
            model.selectedSeqs = Set(selectedSeqs)
            model.selectedLineText = selectedLineText
        }

        @objc func onScrollViewBoundsChanged(_ notification: Notification) {
            let contentView = notification.object as! NSClipView
            let bounds = contentView.bounds
            let maxY = bounds.maxY - inspectorHeight /* scrollView.additionalSafeAreaInsets.bottom */
            let tableView = contentView.documentView as! NSTableView
            let row = tableView.row(at: NSPoint(x: 0, y: maxY))
            lastScrolledSeq = nil
            if row != -1 {
                // .view is less reliable because view might not be created yet?
                model.displayContents.withLock { contents in
                    if row >= contents.lines.count {
                        return
                    }
                    lastScrolledSeq = contents.lines[offset: row].seq
                }
            }
        }

        func copySelected(tableView: NSTableView) {
            // copy all selected lines
            var selectedLines = String()
            model.displayContents.withLock { contents in
                for row in tableView.selectedRowIndexes {
                    if row >= contents.lines.count {
                        continue
                    }
                    if !selectedLines.isEmpty {
                        selectedLines.append("\n")
                    }
                    selectedLines.append(contents.lines[offset: row].text.string)
                }
            }
            NSPasteboard.copy(selectedLines)
        }
    }

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSScrollView()
        scrollView.additionalSafeAreaInsets.top = topInset
        scrollView.additionalSafeAreaInsets.bottom = inspectorHeight
        scrollView.hasVerticalScroller = true

        let tableView = LogsNSTableView()
        tableView.delegate = context.coordinator
        tableView.dataSource = context.coordinator
        scrollView.documentView = tableView

        scrollView.contentView.postsBoundsChangedNotifications = true
        NotificationCenter.default.addObserver(context.coordinator, selector: #selector(Coordinator.onScrollViewBoundsChanged), name: NSView.boundsDidChangeNotification, object: scrollView.contentView)

        tableView.allowsMultipleSelection = true
        tableView.headerView = nil
        tableView.usesAlternatingRowBackgroundColors = false
        tableView.rowHeight = 20  // Set fixed row height for consistent vertical centering

        tableView.menu = RIMenu {
            RIMenuItem("Copy") {
                let clickedRow = tableView.clickedRow
                if clickedRow != -1 {
                    if tableView.selectedRowIndexes.contains(clickedRow) {
                        // copy all selected lines
                        var selectedLines = String()
                        model.displayContents.withLock { contents in
                            for row in tableView.selectedRowIndexes {
                                if row >= contents.lines.count {
                                    continue
                                }
                                if !selectedLines.isEmpty {
                                    selectedLines.append("\n")
                                }
                                selectedLines.append(contents.lines[offset: row].text.string)
                            }
                        }
                        NSPasteboard.copy(selectedLines)
                    } else {
                        if let line = model.displayContents.withLock { contents in
                            if clickedRow >= contents.lines.count {
                                return LogLine?(nil)
                            }
                            return contents.lines[offset: clickedRow]
                        } {
                            NSPasteboard.copy(line.text.string)
                        }
                    }
                }
            }
        }.menu

        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("column"))
        tableView.addTableColumn(column)

        model.updateEvent.throttle(for: .milliseconds(15), scheduler: DispatchQueue.main, latest: true).sink {
            // no overflow risk: this is a float
            let shouldScroll = ((scrollView.contentView.bounds.maxY - scrollView.additionalSafeAreaInsets.bottom) >= tableView.bounds.maxY - bottomScrollThreshold)

            tableView.reloadData()

            // restore selected sequences
            // seq-based is more reliable because it works across searches, where we can't easily just track added+removed rows
            var newSelectedIndexes = IndexSet()
            var lastScrolledRow = -1
            model.displayContents.withLock { contents in
                for (i, line) in contents.lines.enumerated() {
                    if model.selectedSeqs.contains(line.seq) {
                        newSelectedIndexes.insert(i)
                    }
                    if line.seq == context.coordinator.lastScrolledSeq {
                        lastScrolledRow = i
                    }
                }
            }
            tableView.selectRowIndexes(newSelectedIndexes, byExtendingSelection: false)

            if shouldScroll {
                print("scroll to END")
                tableView.scrollRowToVisible(tableView.numberOfRows - 1)
            } else if lastScrolledRow != -1 {
                print("scroll to \(lastScrolledRow)")
                // if NOT at the end, try to restore the scroll position
                tableView.scrollRowToVisible(lastScrolledRow)
            }
        }.store(in: &context.coordinator.cancellables)

        DispatchQueue.main.async {
            tableView.window?.makeFirstResponder(tableView)
        }

        return scrollView
    }

    func updateNSView(_ nsView: NSScrollView, context: Context) {
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(model: model)
    }
}

struct LogsView: View {
    @Environment(\.logsTopInset) private var logsTopInset
    @EnvironmentObject private var commandModel: CommandViewModel

    let cmdExe: String
    let args: [String]
    let extraArgs: [String]
    let extraState: [String]
    @ObservedObject var model: LogsViewModel

    var body: some View {
        LogsTextView(model: model, commandModel: commandModel, topInset: logsTopInset)
        // render under toolbar
        .ignoresSafeArea()
        .safeAreaInset(edge: .bottom) {
            VStack(spacing: 0) {
                Divider()

                VStack {
                    TextEditor(text: .constant(model.selectedLineText))
                        .font(.body.monospaced())
                        .introspect(.textEditor, on: .macOS(.v13, .v14, .v15)) { nsTextView in
                            // SwiftUI .contentMargins resets on unfocus??
                            nsTextView.textContainerInset = NSSize(width: 10, height: 10)
                            nsTextView.isEditable = false
                        }
                }
                .frame(height: inspectorHeight)
            }
        }
        .onAppear {
            model.start(cmdExe: cmdExe, args: args + extraArgs)
        }
        .onDisappear {
            model.stop()
        }
        .onChange(of: args) { _, newArgs in
            model.start(cmdExe: cmdExe, args: newArgs + extraArgs)
        }
        .onChange(of: extraArgs) { _, newExtraArgs in
            model.start(cmdExe: cmdExe, args: args + newExtraArgs, clearAndRestart: true)
        }
        .onChange(of: extraState) { _, _ in
            model.start(cmdExe: cmdExe, args: args + extraArgs, clearAndRestart: true)
        }
        .onChange(of: commandModel.searchField) { _, newSearchField in
            model.setSearchFilter(newSearchField)
        }
    }
}

extension View {
    func toolbar(forCommands commandModel: CommandViewModel, hasSidebar: Bool = false)
        -> some View
    {
        toolbar {
            ToolbarItem(placement: .navigation) {
                // unlike main window, we never use NavigationSplitView b/c sidebar button bug
                // only show sidebar
                if hasSidebar {
                    ToggleSidebarButton()
                }
            }

            ToolbarItem(placement: .automatic) {
                Button {
                    commandModel.copyAllCommand.send()
                } label: {
                    Label("Copy", systemImage: "doc.on.doc")
                }
                .help("Copy")
                .keyboardShortcut("c", modifiers: [.command, .shift])
            }

            ToolbarItem(placement: .automatic) {
                Button {
                    commandModel.clearCommand.send()
                } label: {
                    Label("Clear", systemImage: "trash")
                }
                .help("Clear")
                .keyboardShortcut("k", modifiers: [.command])
            }
        }
    }
}

private class Debouncer {
    private let delay: TimeInterval
    private var handler: () -> Void

    private var timer: Timer?

    init(delay: TimeInterval, handler: @escaping () -> Void) {
        self.delay = delay
        self.handler = handler
    }

    func call() {
        if timer == nil {
            self.handler()

            timer?.invalidate()
            timer = Timer.scheduledTimer(withTimeInterval: delay, repeats: false) { [weak self] _ in
                guard let self else { return }
                self.handler()
                self.timer = nil
            }
        }
    }
}

struct LogsTopInsetKey: EnvironmentKey {
    static let defaultValue: CGFloat = 0
}

extension EnvironmentValues {
    var logsTopInset: CGFloat {
        get { self[LogsTopInsetKey.self] }
        set { self[LogsTopInsetKey.self] = newValue }
    }
}

extension View {
    func logsTopInset(_ inset: CGFloat) -> some View {
        environment(\.logsTopInset, inset)
    }
}
