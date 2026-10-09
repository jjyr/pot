import AppKit

@main struct Tests {
    @MainActor static func main() throws {
        _ = NSApplication.shared
        let idle = trayIcon(cooking: false, phase: 0.5, waiting: false, failed: false, unread: 0, highlighted: false)
        let cooking = trayIcon(cooking: true, phase: 0.5, waiting: false, failed: false, unread: 0, highlighted: false)
        let unread = trayIcon(cooking: false, phase: 0.5, waiting: false, failed: false, unread: 3, highlighted: false)
        let capped = trayIcon(cooking: true, phase: 0.5, waiting: false, failed: false, unread: 10, highlighted: false)
        let many = trayIcon(cooking: true, phase: 0.5, waiting: false, failed: false, unread: 1000, highlighted: false)
        assert(idle.size == cooking.size && idle.size == unread.size && idle.size == many.size)
        assert(idle.tiffRepresentation != cooking.tiffRepresentation) // Static Reduce Motion shapes differ.
        assert(idle.tiffRepresentation != unread.tiffRepresentation) // Idle still shows unread.
        assert(capped.tiffRepresentation == many.tiffRepresentation) // No expanding digits above 9.
        if CommandLine.arguments.count > 1 {
            let sheet = NSImage(size: NSSize(width: 400, height: 128), flipped: false) { _ in
                for (index, appearance) in [NSAppearance.Name.aqua, .darkAqua].enumerated() {
                    NSAppearance(named: appearance)!.performAsCurrentDrawingAppearance {
                        let y = CGFloat(index * 64)
                        (index == 0 ? NSColor.white : NSColor(white: 0.125, alpha: 1)).setFill()
                        NSRect(x: 0, y: y, width: 400, height: 64).fill()
                        for (column, count) in [0, 3, 10, 1000].enumerated() {
                            let icon = trayIcon(cooking: column % 2 == 1, phase: 0.5, waiting: false,
                                                failed: false, unread: count, highlighted: false)
                            icon.draw(in: NSRect(x: CGFloat(column * 100 + 10), y: y + 10, width: 100, height: 44))
                        }
                    }
                }
                return true
            }
            let bitmap = NSBitmapImageRep(data: sheet.tiffRepresentation!)!
            try bitmap.representation(using: .png, properties: [:])!
                .write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
        }
        print("Pot icon: static activity, independent unread, fixed width, 9+ cap passed")
    }
}
