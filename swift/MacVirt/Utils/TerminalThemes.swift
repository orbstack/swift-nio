import AppKit
import SwiftUI

struct TerminalTheme: Equatable, Hashable {
    let palette: [NSColor]

    let background: NSColor
    let foreground: NSColor
    let cursorColor: NSColor
    let cursorText: NSColor
    let selectionBackground: NSColor
    let selectionForeground: NSColor

    private init(
        palette: [NSColor], background: NSColor, foreground: NSColor, cursorColor: NSColor,
        cursorText: NSColor, selectionBackground: NSColor, selectionForeground: NSColor
    ) {
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
        self.palette = palette.map { NSColor(hex: $0) }
        self.background = NSColor(hex: background)
        self.foreground = NSColor(hex: foreground)
        self.cursorColor = NSColor(hex: cursorColor)
        self.cursorText = NSColor(hex: cursorText)
        self.selectionBackground = NSColor(hex: selectionBackground)
        self.selectionForeground = NSColor(hex: selectionForeground)
    }

    private init(palette: [UInt32]) {
        self.palette = palette.map { NSColor(hex: $0) }
        self.background = NSColor.textBackgroundColor
        self.foreground = NSColor.textColor
        self.cursorColor = NSColor.selectedControlColor
        self.cursorText = NSColor.textColor
        self.selectionBackground = NSColor.selectedTextBackgroundColor
        self.selectionForeground = NSColor.textColor
    }

    func toGhosttyArgs() -> [String] {
        var args = [String]()
        for i in 0..<palette.count {
            args.append("--palette=\(i)=\(palette[i].hexString)")
        }
        args.append("--background=\(background.hexString)")
        args.append("--foreground=\(foreground.hexString)")
        args.append("--cursor-color=\(cursorColor.hexString)")
        args.append("--cursor-text=\(cursorText.hexString)")
        args.append("--selection-background=\(selectionBackground.hexString)")
        args.append("--selection-foreground=\(selectionForeground.hexString)")
        return args
    }
}

extension NSColor {
    var hexString: String {
        let components = self.cgColor.components
        return String(
            format: "#%02X%02X%02X", Int(components![0] * 255), Int(components![1] * 255),
            Int(components![2] * 255))
    }
}

extension TerminalTheme {
    static let defaultDark: TerminalTheme = ghosttyAppleSystemColors
    static let defaultLight: TerminalTheme = ghosttyAppleSystemColorsLight

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

    static func forPreference(_ preference: TerminalThemePreference, colorScheme: ColorScheme)
        -> TerminalTheme
    {
        switch preference {
        case .def:  // system
            return colorScheme == .dark ? ghosttyAppleSystemColors : ghosttyAppleSystemColorsLight
        case .rosePine:
            return colorScheme == .dark ? rosePine : rosePineDawn
        }
    }
}
