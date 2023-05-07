//
// Created by Danny Lin on 5/7/23.
//

import Foundation
import SwiftUI
import SwiftTerm

struct SwiftUILocalProcessTerminal: NSViewRepresentable {
    let executable: String
    let args: [String]
    let env: [String]

    func makeNSView(context: Context) -> LocalProcessTerminalViewCustom {
        let view = LocalProcessTerminalViewCustom(frame: NSRect())
        // scrollback increased in SwiftTerm fork
        view.caretColor = NSColor.clear
        view.caretTextColor = NSColor.clear
        view.allowMouseReporting = false
        view.getTerminal().setCursorStyle(.steadyBar)
        view.getTerminal().hideCursor()
        view.configureNativeColors()
        // remove NSScroller subview to fix weird scrollbar
        for subview in view.subviews {
            if subview is NSScroller {
                subview.removeFromSuperview()
            }
        }
        view.startProcess(executable: executable, args: args, environment: env)
        return view
    }

    func updateNSView(_ nsView: LocalProcessTerminalViewCustom, context: Context) {
    }

    static func dismantleNSView(_ nsView: LocalProcessTerminalViewCustom, coordinator: ()) {
        // on close, kill process if still running
        if nsView.process.running {
            // require SwiftTerm fork/PR to avoid crash
            nsView.process.terminate()
        }
    }
}

protocol LocalProcessTerminalViewDelegateCustom: AnyObject {
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
    func hostCurrentDirectoryUpdate (source: TerminalView, directory: String?)

    /**
     * This method will be invoked when the child process started by `startProcess` has terminated.
     * - Parameter source: the local process that terminated
     * - Parameter exitCode: the exit code returned by the process, or nil if this was an error caused during the IO reading/writing
     */
    func processTerminated (source: TerminalView, exitCode: Int32?)
}

class LocalProcessTerminalViewCustom: TerminalView, TerminalViewDelegate, LocalProcessDelegate {

    // MOD: public
    public var process: LocalProcess!

    public override init (frame: CGRect)
    {
        super.init (frame: frame)
        setup ()
    }

    public required init? (coder: NSCoder)
    {
        super.init (coder: coder)
        setup ()
    }

    func setup ()
    {
        terminalDelegate = self
        process = LocalProcess (delegate: self)
    }

    /**
     * The `processDelegate` is used to deliver messages and information relevant t
     */
    public weak var processDelegate: LocalProcessTerminalViewDelegateCustom?

    /**
     * This method is invoked to notify the client of the new columsn and rows that have been set by the UI
     */
    public func sizeChanged(source: TerminalView, newCols: Int, newRows: Int) {
        guard process.running else {
            return
        }
        var size = getWindowSize()
        let _ = PseudoTerminalHelpers.setWinSize(masterPtyDescriptor: process.childfd, windowSize: &size)

        processDelegate?.sizeChanged (source: self, newCols: newCols, newRows: newRows)
    }

    public func clipboardCopy(source: TerminalView, content: Data) {
        if let str = String (bytes: content, encoding: .utf8) {
            let pasteBoard = NSPasteboard.general
            pasteBoard.clearContents()
            pasteBoard.writeObjects([str as NSString])
        }
    }

    /**
     * Invoke this method to notify the processDelegate of the new title for the terminal window
     */
    public func setTerminalTitle(source: TerminalView, title: String) {
        processDelegate?.setTerminalTitle (source: self, title: title)
    }

    public func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {
        processDelegate?.hostCurrentDirectoryUpdate(source: source, directory: directory)
    }


    /**
     * This method is invoked when input from the user needs to be sent to the client
     */
    public func send(source: TerminalView, data: ArraySlice<UInt8>)
    {
        // don't send anything, we don't take input
        //process.send (data: data)
    }

    /**
     * Use this method to toggle the logging of data coming from the host, or pass nil to stop
     */
    public func setHostLogging (directory: String?)
    {
        process.setHostLogging (directory: directory)
    }

    public func scrolled(source: TerminalView, position: Double) {
        // noting
    }

    public func rangeChanged(source: TerminalView, startY: Int, endY: Int) {
        //
    }

    /**
     * Launches a child process inside a pseudo-terminal.
     * - Parameter executable: The executable to launch inside the pseudo terminal, defaults to /bin/bash
     * - Parameter args: an array of strings that is passed as the arguments to the underlying process
     * - Parameter environment: an array of environment variables to pass to the child process, if this is null, this picks a good set of defaults from `Terminal.getEnvironmentVariables`.
     * - Parameter execName: If provided, this is used as the Unix argv[0] parameter, otherwise, the executable is used as the args [0], this is used when the intent is to set a different process name than the file that backs it.
     */
    public func startProcess(executable: String = "/bin/bash", args: [String] = [], environment: [String]? = nil, execName: String? = nil)
    {
        process.startProcess(executable: executable, args: args, environment: environment, execName: execName)
    }

    /**
     * Implements the LocalProcessDelegate method.
     */
    public func processTerminated(_ source: LocalProcess, exitCode: Int32?) {
        processDelegate?.processTerminated(source: self, exitCode: exitCode)
    }

    /**
     * Implements the LocalProcessDelegate.dataReceived method
     */
    public func dataReceived(slice: ArraySlice<UInt8>) {
        feed (byteArray: slice)
    }

    /**
     * Implements the LocalProcessDelegate.getWindowSize method
     */
    public func getWindowSize () -> winsize
    {
        let f: CGRect = self.frame
        // terminal is internal
        //return winsize(ws_row: UInt16(terminal.rows), ws_col: UInt16(terminal.cols), ws_xpixel: UInt16 (f.width), ws_ypixel: UInt16 (f.height))
        return winsize(ws_row: 0, ws_col: 0, ws_xpixel: UInt16 (f.width), ws_ypixel: UInt16 (f.height))
    }
}