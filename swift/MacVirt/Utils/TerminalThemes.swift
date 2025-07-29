import SwiftTerm
import AppKit

struct TerminalTheme: Equatable, Hashable {
    let palette: [SwiftTerm.Color]

    let background: NSColor
    let foreground: NSColor
    let cursorColor: NSColor
    let cursorText: NSColor
    let selectionBackground: NSColor

    // unsupported
    let selectionForeground: NSColor

    private init(palette: [SwiftTerm.Color], background: NSColor, foreground: NSColor, cursorColor: NSColor, cursorText: NSColor, selectionBackground: NSColor, selectionForeground: NSColor) {
        self.palette = palette
        self.background = background
        self.foreground = foreground
        self.cursorColor = cursorColor
        self.cursorText = cursorText
        self.selectionBackground = selectionBackground
        self.selectionForeground = selectionForeground
    }

    private init(
        palette: [UInt32],
        background: UInt32,
        foreground: UInt32,
        cursorColor: UInt32,
        cursorText: UInt32,
        selectionBackground: UInt32,
        selectionForeground: UInt32,
    ) {
        self.palette = palette.map { SwiftTerm.Color(hex: $0) }
        self.background = NSColor(hex: background)
        self.foreground = NSColor(hex: foreground)
        self.cursorColor = NSColor(hex: cursorColor)
        self.cursorText = NSColor(hex: cursorText)
        self.selectionBackground = NSColor(hex: selectionBackground)
        self.selectionForeground = NSColor(hex: selectionForeground)
    }

    private init(palette: [UInt32]) {
        self.palette = palette.map { SwiftTerm.Color(hex: $0) }
        self.background = NSColor.textBackgroundColor
        self.foreground = NSColor.textColor
        self.cursorColor = NSColor.selectedControlColor
        self.cursorText = NSColor.textColor
        self.selectionBackground = NSColor.selectedTextBackgroundColor
        self.selectionForeground = NSColor.textColor
    }
}

private extension SwiftTerm.Color {
    convenience init(hex: UInt32) {
        // 255 -> 65535 scale
        self.init(red: UInt16((hex >> 16) & 0xFF) * 257, green: UInt16((hex >> 8) & 0xFF) * 257, blue: UInt16(hex & 0xFF) * 257)
    }

    // private for some reason
    convenience init(red8: UInt8, green8: UInt8, blue8: UInt8) {
        self.init(red: UInt16(red8) * 257, green: UInt16(green8) * 257, blue: UInt16(blue8) * 257)
    }
}

extension TerminalTheme {
    // nil for no theme
    static let defaultDark: TerminalTheme? = ghosttyAppleSystemColors
    static let defaultLight: TerminalTheme? = ghosttyAppleSystemColorsLight

    // palette from SwiftTerm.Color private
    static let terminalApp = TerminalTheme(
        palette: [
            SwiftTerm.Color(red8: 0, green8: 0, blue8: 0),
            SwiftTerm.Color(red8: 194, green8: 54, blue8: 33),
            SwiftTerm.Color(red8: 37, green8: 188, blue8: 36),
            SwiftTerm.Color(red8: 173, green8: 173, blue8: 39),
            SwiftTerm.Color(red8: 73, green8: 46, blue8: 225),
            SwiftTerm.Color(red8: 211, green8: 56, blue8: 211),
            SwiftTerm.Color(red8: 51, green8: 187, blue8: 200),
            SwiftTerm.Color(red8: 203, green8: 204, blue8: 205),
            SwiftTerm.Color(red8: 129, green8: 131, blue8: 131),
            SwiftTerm.Color(red8: 252, green8: 57, blue8: 31),
            SwiftTerm.Color(red8: 49, green8: 231, blue8: 34),
            SwiftTerm.Color(red8: 234, green8: 236, blue8: 35),
            SwiftTerm.Color(red8: 88, green8: 51, blue8: 255),
            SwiftTerm.Color(red8: 249, green8: 53, blue8: 248),
            SwiftTerm.Color(red8: 20, green8: 240, blue8: 240),
            SwiftTerm.Color(red8: 233, green8: 235, blue8: 235),
        ],
        background: NSColor.textBackgroundColor,
        foreground: NSColor.textColor,
        cursorColor: NSColor.selectedControlColor,
        cursorText: NSColor.textColor,
        selectionBackground: NSColor.selectedTextBackgroundColor,
        selectionForeground: NSColor.textColor,
    )

// palette from SwiftTerm.Color private
    static let swiftTermDefault = TerminalTheme(
        palette: [
            Color (red8: 0, green8: 0, blue8: 0),
            Color (red8: 153, green8: 0, blue8: 1),
            Color (red8: 0, green8: 166, blue8: 3),
            Color (red8: 153, green8: 153, blue8: 0),
            Color (red8: 3, green8: 0, blue8: 178),
            Color (red8: 178, green8: 0, blue8: 178),
            Color (red8: 0, green8: 165, blue8: 178),
            Color (red8: 191, green8: 191, blue8: 191),
            Color (red8: 138, green8: 137, blue8: 138),
            Color (red8: 229, green8: 0, blue8: 1),
            Color (red8: 0, green8: 216, blue8: 0),
            Color (red8: 229, green8: 229, blue8: 0),
            Color (red8: 7, green8: 0, blue8: 254),
            Color (red8: 229, green8: 0, blue8: 229),
            Color (red8: 0, green8: 229, blue8: 229),
            Color (red8: 229, green8: 229, blue8: 229),
        ],
        background: NSColor.textBackgroundColor,
        foreground: NSColor.textColor,
        cursorColor: NSColor.selectedControlColor,
        cursorText: NSColor.textColor,
        selectionBackground: NSColor.selectedTextBackgroundColor,
        selectionForeground: NSColor.textColor,
    )

    // Ghostty: rose-pine
    static let rosePine = TerminalTheme(
        palette: [
            0x26233a,
            0xeb6f92,
            0x31748f,
            0xf6c177,
            0x9ccfd8,
            0xc4a7e7,
            0xebbcba,
            0xe0def4,
            0x6e6a86,
            0xeb6f92,
            0x31748f,
            0xf6c177,
            0x9ccfd8,
            0xc4a7e7,
            0xebbcba,
            0xe0def4,
        ],
        background: 0x191724,
        foreground: 0xe0def4,
        cursorColor: 0xe0def4,
        cursorText: 0x191724,
        selectionBackground: 0x403d52,
        selectionForeground: 0xe0def4,
    )

    // Ghostty: rose-pine-dawn
    static let rosePineDawn = TerminalTheme(
        palette: [
            0xf2e9e1,
            0xb4637a,
            0x286983,
            0xea9d34,
            0x56949f,
            0x907aa9,
            0xd7827e,
            0x575279,
            0x9893a5,
            0xb4637a,
            0x286983,
            0xea9d34,
            0x56949f,
            0x907aa9,
            0xd7827e,
            0x575279,
        ],
        background: 0xfaf4ed,
        foreground: 0x575279,
        cursorColor: 0x575279,
        cursorText: 0xfaf4ed,
        selectionBackground: 0xdfdad9,
        selectionForeground: 0x575279,
    )

    // Ghostty: Apple System Colors
    // the Ghostty ones are a better (especially better contrast) version of the Apple system colors
    static let ghosttyAppleSystemColors = TerminalTheme(
        palette: [
            0x1a1a1a,
            0xcc372e,
            0x26a439,
            0xcdac08,
            0x0869cb,
            0x9647bf,
            0x479ec2,
            0x98989d,
            0x464646,
            0xff453a,
            0x32d74b,
            0xffd60a,
            0x0a84ff,
            0xbf5af2,
            0x76d6ff,
            0xffffff,
        ],
        background: 0x1e1e1e,
        foreground: 0xffffff,
        cursorColor: 0x98989d,
        cursorText: 0xffffff,
        //selectionBackground: 0x3f638b,
        // color-picked from purple orbstack accent selection in logs window
        selectionBackground: 0x785a87,
        selectionForeground: 0xffffff,
    )

    // Ghostty: Apple System Colors Light
    static let ghosttyAppleSystemColorsLight = TerminalTheme(
        palette: [
            0x1a1a1a,
            0xbc4437,
            0x51a148,
            0xc7ad3a,
            0x2e68c5,
            0x8c4bb8,
            0x5e9cbe,
            0x98989d,
            0x464646,
            0xeb5545,
            0x6bd45f,
            0xf8d84a,
            0x3b82f7,
            0xb260ea,
            0x8dd3fb,
            0xffffff,
        ],
        background: 0xfeffff,
        foreground: 0x000000,
        cursorColor: 0x98989d,
        cursorText: 0xffffff,
        //selectionBackground: 0xb4d7ff,
        // color-picked from purple orbstack accent selection in logs window
        selectionBackground: 0xe7cbf5,
        selectionForeground: 0x000000,
    )
}
