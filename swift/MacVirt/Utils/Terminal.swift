//
// Created by Danny Lin on 5/7/23.
//

import Carbon
import Combine
import Defaults
import Foundation
import GhosttyKit
import SwiftUI

struct TerminalView: View {
    let command: String
    let env: [String]

    init(command: String, env: [String] = []) {
        self.command = command
        self.env = env
    }

    var body: some View {
        GeometryReader { geometry in
            TerminalNSViewRepresentable(
                command: command, env: env, size: geometry.size)
        }
    }
}

struct TerminalNSViewRepresentable: NSViewRepresentable {
    let command: String
    let env: [String]

    let size: CGSize

    func makeNSView(context: Context) -> TerminalNSView {
        return TerminalNSView(command: command, env: env)
    }

    func updateNSView(_ nsView: TerminalNSView, context: Context) {
        nsView.sizeDidChange(size)

        nsView.updateConfig(command: command, env: env)
    }
}

class TerminalNSView: NSView {
    @Environment(\.colorScheme) var colorScheme
    @Default(.terminalTheme) var terminalTheme

    var command: String
    var env: [String]

    override var acceptsFirstResponder: Bool {
        return true
    }

    var surface: Ghostty.Surface?

    var mouseShape: Ghostty.Surface.MouseShape = .textCursor
    var focused: Bool = false

    init(command: String, env: [String] = []) {
        self.command = command
        self.env = env

        super.init(frame: NSMakeRect(0, 0, 800, 600))

        createSurface()

        let center = NotificationCenter.default
        center.addObserver(
            self,
            selector: #selector(self.handleCloseSurface),
            name: .ghosttyCloseSurface,
            object: nil
        )

        updateTrackingAreas()
    }

    required init?(coder: NSCoder) {
        fatalError("init(coder:) has not been implemented")
    }

    deinit {
        NotificationCenter.default.removeObserver(self)
    }

    func focusDidChange(_ focused: Bool) {
        self.focused = focused
    }

    func setMouseShape(_ shape: Ghostty.Surface.MouseShape) {
        mouseShape = shape
        window?.invalidateCursorRects(for: self)
    }

    override func resetCursorRects() {
        addCursorRect(bounds, cursor: mouseShape.appKitValue)
    }

    @objc func handleCloseSurface() {
        createSurface()
    }

    private func createSurface() {
        let size = surface?.size ?? CGSize(width: 800, height: 600)
        surface = Ghostty.Surface(app: AppDelegate.shared.ghostty.app, view: self, command: command, env: env, size: size)
    }

    func updateConfig(command: String, env: [String]) {
        if self.command == command && self.env == env {
            return
        }

        self.command = command
        self.env = env

        createSurface()
    }

    func sizeDidChange(_ size: CGSize) {
        // Ghostty wants to know the actual framebuffer size... It is very important
        // here that we use "size" and NOT the view frame. If we're in the middle of
        // an animation (i.e. a fullscreen animation), the frame will not yet be updated.
        // The size represents our final size we're going for.
        let scaledSize = self.convertToBacking(size)
        surface?.setSize(width: UInt32(scaledSize.width), height: UInt32(scaledSize.height))
    }

    private func setSurfaceSize(width: UInt32, height: UInt32) {
        surface?.setSize(width: width, height: height)
    }

    override func keyUp(with event: NSEvent) {
        _ = keyAction(GHOSTTY_ACTION_RELEASE, event: event)
    }

    override func updateTrackingAreas() {
            // To update our tracking area we just recreate it all.
            trackingAreas.forEach { removeTrackingArea($0) }

            // This tracking area is across the entire frame to notify us of mouse movements.
            addTrackingArea(NSTrackingArea(
                rect: frame,
                options: [
                    .mouseEnteredAndExited,
                    .mouseMoved,

                    // Only send mouse events that happen in our visible (not obscured) rect
                    .inVisibleRect,

                    // We want active always because we want to still send mouse reports
                    // even if we're not focused or key.
                    .activeAlways,
                ],
                owner: self,
                userInfo: nil))
        }

    private func keyAction(
        _ action: ghostty_input_action_e,
        event: NSEvent,
        translationEvent: NSEvent? = nil,
        text: String? = nil,
        composing: Bool = false
    ) -> Bool {
        guard let surface = surface else { return false }

        var key_ev = ghosttyKeyEvent(
            event, action, translationMods: translationEvent?.modifierFlags)
        key_ev.composing = composing

        // For text, we only encode UTF8 if we don't have a single control
        // character. Control characters are encoded by Ghostty itself.
        // Without this, `ctrl+enter` does the wrong thing.
        if let text, text.count > 0,
            let codepoint = text.utf8.first, codepoint >= 0x20
        {
            return text.withCString { ptr in
                key_ev.text = ptr
                return surface.key(key_ev)
            }
        } else {
            return surface.key(key_ev)
        }
    }

    func ghosttyKeyEvent(
        _ event: NSEvent,
        _ action: ghostty_input_action_e,
        translationMods: NSEvent.ModifierFlags? = nil
    ) -> ghostty_input_key_s {
        var key_ev: ghostty_input_key_s = .init()
        key_ev.action = action
        key_ev.keycode = UInt32(event.keyCode)

        // We can't infer or set these safely from this method. Since text is
        // a cString, we can't use self.characters because of garbage collection.
        // We have to let the caller handle this.
        key_ev.text = nil
        key_ev.composing = false

        // macOS provides no easy way to determine the consumed modifiers for
        // producing text. We apply a simple heuristic here that has worked for years
        // so far: control and command never contribute to the translation of text,
        // assume everything else did.
        key_ev.mods = Ghostty.ghosttyMods(event.modifierFlags)
        key_ev.consumed_mods = Ghostty.ghosttyMods(
            (translationMods ?? event.modifierFlags)
                .subtracting([.control, .command]))

        // Our unshifted codepoint is the codepoint with no modifiers. We
        // ignore multi-codepoint values. We have to use `byApplyingModifiers`
        // instead of `charactersIgnoringModifiers` because the latter changes
        // behavior with ctrl pressed and we don't want any of that.
        key_ev.unshifted_codepoint = 0
        if event.type == .keyDown || event.type == .keyUp {
            if let chars = event.characters(byApplyingModifiers: []),
                let codepoint = chars.unicodeScalars.first
            {
                key_ev.unshifted_codepoint = codepoint.value
            }
        }

        return key_ev
    }

    var keyTextAccumulator: [String]? = nil
    private var markedText: NSMutableAttributedString = NSMutableAttributedString()

    /// Records the timestamp of the last event to performKeyEquivalent that we need to save.
    /// We currently save all commands with command or control set.
    ///
    /// For command+key inputs, the AppKit input stack calls performKeyEquivalent to give us a chance
    /// to handle them first. If we return "false" then it goes through the standard AppKit responder chain.
    /// For an NSTextInputClient, that may redirect some commands _before_ our keyDown gets called.
    /// Concretely: Command+Period will do: performKeyEquivalent, doCommand ("cancel:"). In doCommand,
    /// we need to know that we actually want to handle that in keyDown, so we send it back through the
    /// event dispatch system and use this timestamp as an identity to know to actually send it to keyDown.
    ///
    /// Why not send it to keyDown always? Because if the user rebinds a command to something we
    /// actually handle then we do want the standard response chain to handle the key input. Unfortunately,
    /// we can't know what a command is bound to at a system level until we let it flow through the system.
    /// That's the crux of the problem.
    ///
    /// So, we have to send it back through if we didn't handle it.
    ///
    /// The next part of the problem is comparing NSEvent identity seems pretty nasty. I couldn't
    /// find a good way to do it. I originally stored a weak ref and did identity comparison but that
    /// doesn't work and for reasons I couldn't figure out the value gets mangled (fields don't match
    /// before/after the assignment). I suspect it has something to do with the fact an NSEvent is wrapping
    /// a lower level event pointer and its just not surviving the Swift runtime somehow. I don't know.
    ///
    /// The best thing I could find was to store the event timestamp which has decent granularity
    /// and compare that. To further complicate things, some events are synthetic and have a zero
    /// timestamp so we have to protect against that. Fun!
    var lastPerformKeyEvent: TimeInterval?

    override func keyDown(with event: NSEvent) {
        NSLog("keydown: \(event.characters ?? "nil")")

        guard let surface = self.surface else {
            self.interpretKeyEvents([event])
            NSLog("surface is nil")
            return
        }

        // On any keyDown event we unset our bell state
        // bell = false

        // We need to translate the mods (maybe) to handle configs such as option-as-alt
        let translationModsGhostty = Ghostty.eventModifierFlags(
            mods: ghostty_surface_key_translation_mods(
                surface.surface,
                Ghostty.ghosttyMods(event.modifierFlags)
            )
        )

        // There are hidden bits set in our event that matter for certain dead keys
        // so we can't use translationModsGhostty directly. Instead, we just check
        // for exact states and set them.
        var translationMods = event.modifierFlags
        for flag in [NSEvent.ModifierFlags.shift, .control, .option, .command] {
            if translationModsGhostty.contains(flag) {
                translationMods.insert(flag)
            } else {
                translationMods.remove(flag)
            }
        }

        // If the translation modifiers are not equal to our original modifiers
        // then we need to construct a new NSEvent. If they are equal we reuse the
        // old one. IMPORTANT: we MUST reuse the old event if they're equal because
        // this keeps things like Korean input working. There must be some object
        // equality happening in AppKit somewhere because this is required.
        let translationEvent: NSEvent
        if translationMods == event.modifierFlags {
            translationEvent = event
        } else {
            translationEvent =
                NSEvent.keyEvent(
                    with: event.type,
                    location: event.locationInWindow,
                    modifierFlags: translationMods,
                    timestamp: event.timestamp,
                    windowNumber: event.windowNumber,
                    context: nil,
                    characters: event.characters(byApplyingModifiers: translationMods) ?? "",
                    charactersIgnoringModifiers: event.charactersIgnoringModifiers ?? "",
                    isARepeat: event.isARepeat,
                    keyCode: event.keyCode
                ) ?? event
        }

        let action = event.isARepeat ? GHOSTTY_ACTION_REPEAT : GHOSTTY_ACTION_PRESS

        // By setting this to non-nil, we note that we're in a keyDown event. From here,
        // we call interpretKeyEvents so that we can handle complex input such as Korean
        // language.
        keyTextAccumulator = []
        defer { keyTextAccumulator = nil }

        // We need to know what the length of marked text was before this event to
        // know if these events cleared it.
        let markedTextBefore = markedText.length > 0

        // We need to know the keyboard layout before below because some keyboard
        // input events will change our keyboard layout and we don't want those
        // going to the terminal.
        let keyboardIdBefore: String? =
            if !markedTextBefore {
                KeyboardLayout.id
            } else {
                nil
            }

        // If we are in a keyDown then we don't need to redispatch a command-modded
        // key event (see docs for this field) so reset this to nil because
        // `interpretKeyEvents` may dispach it.
        self.lastPerformKeyEvent = nil

        self.interpretKeyEvents([translationEvent])

        // If our keyboard changed from this we just assume an input method
        // grabbed it and do nothing.
        if !markedTextBefore && keyboardIdBefore != KeyboardLayout.id {
            return
        }

        // If we have marked text, we're in a preedit state. The order we
        // do this and the key event callbacks below doesn't matter since
        // we control the preedit state only through the preedit API.
        syncPreedit(clearIfNeeded: markedTextBefore)

        NSLog("keydown about to send")

        if let list = keyTextAccumulator, list.count > 0 {
            // If we have text, then we've composed a character, send that down.
            // These never have "composing" set to true because these are the
            // result of a composition.
            for text in list {
                _ = keyAction(
                    action,
                    event: event,
                    translationEvent: translationEvent,
                    text: text
                )
            }
        } else {
            // We have no accumulated text so this is a normal key event.
            _ = keyAction(
                action,
                event: event,
                translationEvent: translationEvent,
                text: ghosttyCharacters(event: translationEvent),

                // We're composing if we have preedit (the obvious case). But we're also
                // composing if we don't have preedit and we had marked text before,
                // because this input probably just reset the preedit state. It shouldn't
                // be encoded. Example: Japanese begin composing, the press backspace.
                // This should only cancel the composing state but not actually delete
                // the prior input characters (prior to the composing).
                composing: markedText.length > 0 || markedTextBefore
            )
        }
    }

    override func scrollWheel(with event: NSEvent) {
            guard let surface else { return }

            var x = event.scrollingDeltaX
            var y = event.scrollingDeltaY
            let precision = event.hasPreciseScrollingDeltas
            
            if precision {
                // We do a 2x speed multiplier. This is subjective, it "feels" better to me.
                x *= 2;
                y *= 2;

                // TODO(mitchellh): do we have to scale the x/y here by window scale factor?
            }

            let scrollEvent = Ghostty.Surface.MouseScrollEvent(
                x: x,
                y: y,
                mods: .init(precision: precision, momentum: .init(event.momentumPhase))
            )
            surface.sendMouseScroll(scrollEvent)
        }

        override func mouseDown(with event: NSEvent) {
           guard let surface else { return }

           let mods = Ghostty.Surface.InputMods(nsFlags: event.modifierFlags)
           surface.sendMouseButton(.left, .pressed, mods)
        }

        override func mouseUp(with event: NSEvent) {
            guard let surface else { return }

            let mods = Ghostty.Surface.InputMods(nsFlags: event.modifierFlags)
            surface.sendMouseButton(.left, .released, mods)
        }

        override func rightMouseDown(with event: NSEvent) {
            guard let surface else { return }

            let mods = Ghostty.Surface.InputMods(nsFlags: event.modifierFlags)
            surface.sendMouseButton(.right, .pressed, mods)
        }

        override func rightMouseUp(with event: NSEvent) {
            guard let surface else { return }

            let mods = Ghostty.Surface.InputMods(nsFlags: event.modifierFlags)
            surface.sendMouseButton(.right, .released, mods)
        }
        
        override func otherMouseDown(with event: NSEvent) {
            guard let surface else { return }
            guard event.buttonNumber == 2 else { return }

            let mods = Ghostty.Surface.InputMods(nsFlags: event.modifierFlags)
            surface.sendMouseButton(.middle, .pressed, mods)
        }

        override func otherMouseUp(with event: NSEvent) {
            guard let surface else { return }
            guard event.buttonNumber == 2 else { return }

            let mods = Ghostty.Surface.InputMods(nsFlags: event.modifierFlags)
            surface.sendMouseButton(.middle, .released, mods)
        }

        override func mouseEntered(with event: NSEvent) {
            self.mouseShape = .textCursor
            self.mouseMoved(with: event)
        }

        override func mouseExited(with event: NSEvent) {
            self.mouseMoved(with: event)
        }

        override func mouseDragged(with event: NSEvent) {
            self.mouseMoved(with: event)
        }
        
        override func rightMouseDragged(with event: NSEvent) {
            self.mouseMoved(with: event)
        }

        override func otherMouseDragged(with event: NSEvent) {
            self.mouseMoved(with: event)
        }

        override func mouseMoved(with event: NSEvent) {
            guard let surface else { return }

            let pos = self.convert(event.locationInWindow, from: nil)
            surface.sendMousePos(Ghostty.Surface.MousePosEvent(
                x: pos.x,
                y: frame.height - pos.y,
                mods: .init(nsFlags: event.modifierFlags)
            ))
        }
}

extension TerminalNSView: NSTextInputClient {
    func hasMarkedText() -> Bool {
        return markedText.length > 0
    }

    func markedRange() -> NSRange {
        guard markedText.length > 0 else { return NSRange() }
        return NSRange(0...(markedText.length-1))
    }

    func selectedRange() -> NSRange {
        guard let surface = self.surface else { return NSRange() }

        // Get our range from the Ghostty API. There is a race condition between getting the
        // range and actually using it since our selection may change but there isn't a good
        // way I can think of to solve this for AppKit.
        return surface.selectedText().range()
    }

    func setMarkedText(_ string: Any, selectedRange: NSRange, replacementRange: NSRange) {
        switch string {
        case let v as NSAttributedString:
            self.markedText = NSMutableAttributedString(attributedString: v)

        case let v as String:
            self.markedText = NSMutableAttributedString(string: v)

        default:
            print("unknown marked text: \(string)")
        }

        // If we're not in a keyDown event, then we want to update our preedit
        // text immediately. This can happen due to external events, for example
        // changing keyboard layouts while composing: (1) set US intl (2) type '
        // to enter dead key state (3)
        if keyTextAccumulator == nil {
            syncPreedit()
        }
    }

    func unmarkText() {
        if self.markedText.length > 0 {
            self.markedText.mutableString.setString("")
            syncPreedit()
        }
    }

    func validAttributesForMarkedText() -> [NSAttributedString.Key] {
        return []
    }

    func attributedSubstring(forProposedRange range: NSRange, actualRange: NSRangePointer?) -> NSAttributedString? {
        // Ghostty.logger.warning("pressure substring range=\(range) selectedRange=\(self.selectedRange())")
        guard let surface = self.surface else { return nil }

        // If the range is empty then we don't need to return anything
        guard range.length > 0 else { return nil }

        // I used to do a bunch of testing here that the range requested matches the
        // selection range or contains it but a lot of macOS system behaviors request
        // bogus ranges I truly don't understand so we just always return the
        // attributed string containing our selection which is... weird but works?

        // Get our selection text
        let text = surface.selectedText()

        // If we can get a font then we use the font. This should always work
        // since we always have a primary font. The only scenario this doesn't
        // work is if someone is using a non-CoreText build which would be
        // unofficial.
        var attributes: [ NSAttributedString.Key : Any ] = [:];
        if let font = surface.quicklookFont() {
            attributes[.font] = font
        }

        return .init(string: text.string(), attributes: attributes)
    }

    func characterIndex(for point: NSPoint) -> Int {
        return 0
    }

    func firstRect(forCharacterRange range: NSRange, actualRange: NSRangePointer?) -> NSRect {
        guard let surface = self.surface else {
            return NSMakeRect(frame.origin.x, frame.origin.y, 0, 0)
        }

        // Ghostty will tell us where it thinks an IME keyboard should render.
        var x: Double = 0;
        var y: Double = 0;

        // QuickLook never gives us a matching range to our selection so if we detect
        // this then we return the top-left selection point rather than the cursor point.
        // This is hacky but I can't think of a better way to get the right IME vs. QuickLook
        // point right now. I'm sure I'm missing something fundamental...
        if range.length > 0 && range != self.selectedRange() {
            // QuickLook
            let text = surface.selectedText()
            if text.range().length > 0 {
                // The -2/+2 here is subjective. QuickLook seems to offset the rectangle
                // a bit and I think these small adjustments make it look more natural.
                (x, y) = text.topLeftCoords()
            } else {
                (x, y) = surface.imePoint()
            }
        } else {
            (x, y) = surface.imePoint()
        }

        // Ghostty coordinates are in top-left (0, 0) so we have to convert to
        // bottom-left since that is what UIKit expects
        let viewRect = NSMakeRect(x, frame.size.height - y, 0, 0)

        // Convert the point to the window coordinates
        let winRect = self.convert(viewRect, to: nil)

        // Convert from view to screen coordinates
        guard let window = self.window else { return winRect }
        return window.convertToScreen(winRect)
    }

    func insertText(_ string: Any, replacementRange: NSRange) {
        // We must have an associated event
        guard NSApp.currentEvent != nil else { return }
        guard let surface else { return }

        // We want the string view of the any value
        var chars = ""
        switch (string) {
        case let v as NSAttributedString:
            chars = v.string
        case let v as String:
            chars = v
        default:
            return
        }

        // If insertText is called, our preedit must be over.
        unmarkText()

        // If we have an accumulator we're in another key event so we just
        // accumulate and return.
        if var acc = keyTextAccumulator {
            acc.append(chars)
            keyTextAccumulator = acc
            return
        }

        surface.sendText(chars)
    }

    /// This function needs to exist for two reasons:
    /// 1. Prevents an audible NSBeep for unimplemented actions.
    /// 2. Allows us to properly encode super+key input events that we don't handle
    override func doCommand(by selector: Selector) {
        // If we are being processed by performKeyEquivalent with a command binding,
        // we send it back through the event system so it can be encoded.
        if let lastPerformKeyEvent,
           let current = NSApp.currentEvent,
           lastPerformKeyEvent == current.timestamp
        {
            NSApp.sendEvent(current)
            return
        }

        print("SEL: \(selector)")
    }

    /// Sync the preedit state based on the markedText value to libghostty
    private func syncPreedit(clearIfNeeded: Bool = true) {
        guard let surface else { return }

        if markedText.length > 0 {
            let str = markedText.string
            if !str.isEmpty {
                surface.preEdit(str)
            }
        } else if clearIfNeeded {
            // If we had marked text before but don't now, we're no longer
            // in a preedit state so we can clear it.
            surface.preEdit(nil)
        }
    }

        override func flagsChanged(with event: NSEvent) {
            let mod: UInt32;
            switch (event.keyCode) {
            case 0x39: mod = GHOSTTY_MODS_CAPS.rawValue
            case 0x38, 0x3C: mod = GHOSTTY_MODS_SHIFT.rawValue
            case 0x3B, 0x3E: mod = GHOSTTY_MODS_CTRL.rawValue
            case 0x3A, 0x3D: mod = GHOSTTY_MODS_ALT.rawValue
            case 0x37, 0x36: mod = GHOSTTY_MODS_SUPER.rawValue
            default: return
            }

            // If we're in the middle of a preedit, don't do anything with mods.
            if hasMarkedText() { return }

            // The keyAction function will do this AGAIN below which sucks to repeat
            // but this is super cheap and flagsChanged isn't that common.
            let mods = Ghostty.ghosttyMods(event.modifierFlags)

            // If the key that pressed this is active, its a press, else release.
            var action = GHOSTTY_ACTION_RELEASE
            if (mods.rawValue & mod != 0) {
                // If the key is pressed, its slightly more complicated, because we
                // want to check if the pressed modifier is the correct side. If the
                // correct side is pressed then its a press event otherwise its a release
                // event with the opposite modifier still held.
                let sidePressed: Bool
                switch (event.keyCode) {
                case 0x3C:
                    sidePressed = event.modifierFlags.rawValue & UInt(NX_DEVICERSHIFTKEYMASK) != 0;
                case 0x3E:
                    sidePressed = event.modifierFlags.rawValue & UInt(NX_DEVICERCTLKEYMASK) != 0;
                case 0x3D:
                    sidePressed = event.modifierFlags.rawValue & UInt(NX_DEVICERALTKEYMASK) != 0;
                case 0x36:
                    sidePressed = event.modifierFlags.rawValue & UInt(NX_DEVICERCMDKEYMASK) != 0;
                default:
                    sidePressed = true
                }

                if (sidePressed) {
                    action = GHOSTTY_ACTION_PRESS
                }
            }

            _ = keyAction(action, event: event)
        }
}

class KeyboardLayout {
    /// Return a string ID of the current keyboard input source.
    static var id: String? {
        if let source = TISCopyCurrentKeyboardInputSource()?.takeRetainedValue(),
            let sourceIdPointer = TISGetInputSourceProperty(source, kTISPropertyInputSourceID)
        {
            let sourceId = unsafeBitCast(sourceIdPointer, to: CFString.self)
            return sourceId as String
        }

        return nil
    }
}

func ghosttyCharacters(event: NSEvent) -> String? {
    // If we have no characters associated with this event we do nothing.
    guard let characters = event.characters else { return nil }

    if characters.count == 1,
        let scalar = characters.unicodeScalars.first
    {
        // If we have a single control character, then we return the characters
        // without control pressed. We do this because we handle control character
        // encoding directly within Ghostty's KeyEncoder.
        if scalar.value < 0x20 {
            return event.characters(byApplyingModifiers: event.modifierFlags.subtracting(.control))
        }

        // If we have a single value in the PUA, then it's a function key and
        // we don't want to send PUA ranges down to Ghostty.
        if scalar.value >= 0xF700 && scalar.value <= 0xF8FF {
            return nil
        }
    }

    return characters
}

extension Array where Element == String {
    /// Executes a closure with an array of C string pointers.
    func withCStrings<T>(_ body: ([UnsafePointer<Int8>?]) throws -> T) rethrows -> T {
        // Handle empty array
        if isEmpty {
            return try body([])
        }

        // Recursive helper to process strings
        func helper(
            index: Int, accumulated: [UnsafePointer<Int8>?],
            body: ([UnsafePointer<Int8>?]) throws -> T
        ) rethrows -> T {
            if index == count {
                return try body(accumulated)
            }

            return try self[index].withCString { cStr in
                var newAccumulated = accumulated
                newAccumulated.append(cStr)
                return try helper(index: index + 1, accumulated: newAccumulated, body: body)
            }
        }

        return try helper(index: 0, accumulated: [], body: body)
    }
}

extension Optional where Wrapped == String {
    /// Executes a closure with a C string pointer, handling nil gracefully.
    func withCString<T>(_ body: (UnsafePointer<Int8>?) throws -> T) rethrows -> T {
        if let string = self {
            return try string.withCString(body)
        } else {
            return try body(nil)
        }
    }
}
