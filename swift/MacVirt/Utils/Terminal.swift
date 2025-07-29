//
// Created by Danny Lin on 5/7/23.
//

import Combine
import Defaults
import Foundation
import SwiftTerm
import SwiftUI

class TerminalViewModel: ObservableObject {
    @Published var windowSize = CGSize.zero

    let clearCommand = PassthroughSubject<(), Never>()
    let copyAllCommand = PassthroughSubject<(), Never>()
}

class LocalProcessTerminalController: NSViewController {
    // we use controller so we can store cancellable state
    private let model: TerminalViewModel
    private var cancellables = Set<AnyCancellable>()

    private var lastTheme: TerminalTheme?

    private var terminalView: LocalProcessTerminalViewCustom {
        view as! LocalProcessTerminalViewCustom
    }

    init(model: TerminalViewModel) {
        self.model = model
        super.init(nibName: nil, bundle: nil)
    }

    required init?(coder: NSCoder) {
        fatalError()
    }

    override func loadView() {
        let view = LocalProcessTerminalViewCustom(frame: .zero)
        // scrollback increased in SwiftTerm fork
        // 5000 lines, not 25000, due to poor resize performance with large windows
        // reduce idle frame updates
        view.getTerminal().setCursorStyle(.steadyBlock)
        // remove NSScroller subview to fix weird broken scrollbar
        // ghostty still doesn't even have scrollbar so this is fine
        for subview in view.subviews {
            if subview is NSScroller {
                subview.removeFromSuperview()
            }
        }

        self.view = view
    }

    func startProcess(executable: String, args: [String], environment: [String]) {
        terminalView.startProcess(executable: executable, args: args, environment: environment)
    }

    func updateTheme(_ theme: TerminalTheme) {
        if lastTheme != theme {
            lastTheme = theme
            terminalView.installTheme(theme)
        }
    }

    func dismantle() {
        // on close, kill process if still running
        if let process = terminalView.process, process.running {
            // require SwiftTerm fork/PR to avoid crash
            kill(process.shellPid, SIGKILL)
        }
    }
}

struct TerminalTabView: View {
    @Environment(\.colorScheme) var colorScheme
    @Default(.terminalTheme) var terminalTheme

    @StateObject private var terminalModel = TerminalViewModel()

    let executable: String
    let args: [String]
    let env: [String]

    init(executable: String, args: [String], env: [String] = []) {
        self.executable = executable
        self.args = args
        self.env = env
    }

    var body: some View {
        let theme = TerminalTheme.forPreference(terminalTheme, colorScheme: colorScheme)

        SwiftUILocalProcessTerminal(
            executable: executable, args: args, env: env, model: terminalModel, theme: theme
        )
        // otherwise terminal leaks behind toolbar when scrolled
        .clipped()
        // padding that matches terminal bg color
        // this causes toolbar to match bg color, so remove top padding -- it looks like toolbar padding contributes to vertical spacing
        .padding(.horizontal, 8)
        .padding(.bottom, 8)
        .background(Color(theme.background))
    }
}

private struct SwiftUILocalProcessTerminal: NSViewControllerRepresentable {
    let executable: String
    let args: [String]
    let env: [String]
    let model: TerminalViewModel

    let theme: TerminalTheme

    func makeNSViewController(context: Context) -> LocalProcessTerminalController {
        return LocalProcessTerminalController(model: model)
    }

    func updateNSViewController(_ controller: LocalProcessTerminalController, context: Context) {
        controller.startProcess(executable: executable, args: args, environment: env)
        controller.updateTheme(theme)
    }

    static func dismantleNSViewController(
        _ controller: LocalProcessTerminalController, coordinator: ()
    ) {
        controller.dismantle()
    }
}

/// Delegate for the ``LocalProcessTerminalView`` class that is used to
/// notify the user of process-related changes.
public protocol LocalProcessTerminalViewCustomDelegate: AnyObject {
    /**
     * This method is invoked to notify that the terminal has been resized to the specified number of columns and rows
     * the user interface code might try to adjut the containing scroll view, or if it is a toplevel window, the window itself
     * - Parameter source: the sending instance
     * - Parameter newCols: the new number of columns that should be shown
     * - Parameter newRow: the new number of rows that should be shown
     */
    func sizeChanged(source: LocalProcessTerminalViewCustom, newCols: Int, newRows: Int)

    /**
     * This method is invoked when the title of the terminal window should be updated to the provided title
     * - Parameter source: the sending instance
     * - Parameter title: the desired title
     */
    func setTerminalTitle(source: LocalProcessTerminalViewCustom, title: String)

    /**
     * Invoked when the OSC command 7 for "current directory has changed" command is sent
     * - Parameter source: the sending instance
     * - Parameter directory: the new working directory
     */
    func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?)

    /**
     * This method will be invoked when the child process started by `startProcess` has terminated.
     * - Parameter source: the local process that terminated
     * - Parameter exitCode: the exit code returned by the process, or nil if this was an error caused during the IO reading/writing
     */
    func processTerminated(source: TerminalView, exitCode: Int32?)
}

/// `LocalProcessTerminalView` is an AppKit NSView that can be used to host a local process
/// the process is launched inside a pseudo-terminal.
///
/// Call the `startProcess` to launch the underlying process inside a pseudo terminal.
///
/// Generally, for the `LocalProcessTerminalView` to be useful, you will want to disable the sandbox
/// for your application, otherwise the underlying shell will not have access to much - not the majority of
/// commands, not assorted places on the file systems and so on.   For this, you need to disable for your
/// target in "Signing and Capabilities" the sandbox entirely.
///
/// Note: instances of `LocalProcessTerminalView` will set the `TerminalView`'s `delegate`
/// property and capture and consume the messages.   The messages that are most likely needed for
/// consumer applications are reposted to the `LocalProcessTerminalViewDelegate` in
/// `processDelegate`.   If you override the `delegate` directly, you might inadvertently break
/// the internal working of `LocalProcessTerminalView`.   If you must change the `delegate`
/// make sure that you proxy the values in your implementation to the values set after initializing this instance.
///
/// If you want additional control over the delegate methods implemented in this class, you can
/// subclass this and override the methods
open class LocalProcessTerminalViewCustom: TerminalView, TerminalViewDelegate, LocalProcessDelegate
{
    var process: LocalProcess?

    private var pendingProcessRestart = false
    private var lastExecutable: String?
    private var lastArgs: [String]?
    private var lastEnvironment: [String]?

    public override init(frame: CGRect) {
        super.init(frame: frame)
        terminalDelegate = self
    }

    public required init?(coder: NSCoder) {
        super.init(coder: coder)
        terminalDelegate = self
    }

    /**
     * The `processDelegate` is used to deliver messages and information relevant t
     */
    public weak var processDelegate: LocalProcessTerminalViewCustomDelegate?

    /**
     * This method is invoked to notify the client of the new columsn and rows that have been set by the UI
     */
    public func sizeChanged(source: TerminalView, newCols: Int, newRows: Int) {
        guard let process, process.running else {
            return
        }
        var size = getWindowSize()
        let _ = PseudoTerminalHelpers.setWinSize(
            masterPtyDescriptor: process.childfd, windowSize: &size)

        processDelegate?.sizeChanged(source: self, newCols: newCols, newRows: newRows)
    }

    public func clipboardCopy(source: TerminalView, content: Data) {
        if let str = String(bytes: content, encoding: .utf8) {
            let pasteBoard = NSPasteboard.general
            pasteBoard.clearContents()
            pasteBoard.writeObjects([str as NSString])
        }
    }

    /**
     * Invoke this method to notify the processDelegate of the new title for the terminal window
     */
    public func setTerminalTitle(source: TerminalView, title: String) {
        processDelegate?.setTerminalTitle(source: self, title: title)
    }

    public func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {
        processDelegate?.hostCurrentDirectoryUpdate(source: source, directory: directory)
    }

    /**
     * This method is invoked when input from the user needs to be sent to the client
     * Implementation of the TerminalViewDelegate method
     */
    open func send(source: TerminalView, data: ArraySlice<UInt8>) {
        process?.send(data: data)
    }

    /**
     * Use this method to toggle the logging of data coming from the host, or pass nil to stop
     */
    public func setHostLogging(directory: String?) {
        process?.setHostLogging(directory: directory)
    }

    /// Implementation of the TerminalViewDelegate method
    open func scrolled(source: TerminalView, position: Double) {
        // noting
    }

    open func rangeChanged(source: TerminalView, startY: Int, endY: Int) {
        //
    }

    public func bell(source: TerminalView) {
        // nothing (disables bell)
    }

    /**
     * Launches a child process inside a pseudo-terminal.
     * - Parameter executable: The executable to launch inside the pseudo terminal, defaults to /bin/bash
     * - Parameter args: an array of strings that is passed as the arguments to the underlying process
     * - Parameter environment: an array of environment variables to pass to the child process, if this is null, this picks a good set of defaults from `Terminal.getEnvironmentVariables`.
     * - Parameter execName: If provided, this is used as the Unix argv[0] parameter, otherwise, the executable is used as the args [0], this is used when the intent is to set a different process name than the file that backs it.
     */
    public func startProcess(
        executable: String = "/bin/bash", args: [String] = [], environment: [String]? = nil,
        execName: String? = nil
    ) {
        if executable != lastExecutable || args != lastArgs || environment != lastEnvironment {
            lastExecutable = executable
            lastArgs = args
            lastEnvironment = environment

            window?.makeFirstResponder(self)

            if let process, process.running {
                kill(process.shellPid, SIGKILL)

                // set process to be started once old process ends
                pendingProcessRestart = true
            } else {
                startProcessNow(
                    executable: executable, args: args, environment: environment, execName: execName
                )
            }
        }
    }

    private func startProcessNow(
        executable: String, args: [String], environment: [String]?, execName: String?
    ) {
        // combine with default env
        let finalEnv = Terminal.getEnvironmentVariables() + (environment ?? [])

        // resolve deadlock with libBacktraceRecording.dylib's fork_prepare_handler trying to submit to GCD main queue
        Task {
            let newProcess = LocalProcess(delegate: self)
            newProcess.startProcess(
                executable: executable, args: args, environment: finalEnv, execName: execName)
            self.process = newProcess
        }
    }

    /**
     * Implements the LocalProcessDelegate method.
     */
    open func processTerminated(_ source: LocalProcess, exitCode: Int32?) {
        processDelegate?.processTerminated(source: self, exitCode: exitCode)

        if pendingProcessRestart {
            clearAndReset()

            startProcessNow(
                executable: lastExecutable!, args: lastArgs!, environment: lastEnvironment,
                execName: nil)
            pendingProcessRestart = false
        }
    }

    func clearAndReset() {
        // TODO: reset cursor pos
        getTerminal().resetToInitialState()
        // invalidate
        setNeedsDisplay(bounds)
    }

    /**
     * Implements the LocalProcessDelegate.dataReceived method
     */
    open func dataReceived(slice: ArraySlice<UInt8>) {
        feed(byteArray: slice)
    }

    /**
     * Implements the LocalProcessDelegate.getWindowSize method
     */
    open func getWindowSize() -> winsize {
        let f: CGRect = self.frame
        return winsize(
            ws_row: UInt16(getTerminal().rows), ws_col: UInt16(getTerminal().cols),
            ws_xpixel: UInt16(f.width), ws_ypixel: UInt16(f.height))
    }

    // initial startProcess is before viewDidMoveToWindow
    public override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        // make sure we're the first responder
        window?.makeFirstResponder(self)
    }

    func installTheme(_ theme: TerminalTheme) {
        self.installColors(theme.palette)
        self.nativeBackgroundColor = theme.background
        self.nativeForegroundColor = theme.foreground
        self.selectedTextBackgroundColor = theme.selectionBackground
        self.caretColor = theme.cursorColor
        self.caretTextColor = theme.cursorText
    }
}
