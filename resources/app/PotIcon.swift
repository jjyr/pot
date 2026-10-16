import AppKit

@MainActor func potIcon(cooking: Bool = false, phase: Double = 0.5, waiting: Bool = false,
                        color: NSColor = .labelColor) -> NSImage
{
    let image = NSImage(size: NSSize(width: 22, height: 22), flipped: false) { _ in
        companionIcon(blink: waiting ? 0.15 : 1, smile: waiting ? -0.5 : 0,
                      steam: cooking ? 1 : 0, phase: phase, monochrome: color)
            .draw(in: NSRect(x: 0, y: 0, width: 22, height: 22))
        return true
    }
    image.isTemplate = true
    return image
}

@MainActor func trayIcon(cooking: Bool, phase: Double, waiting: Bool, failed: Bool, unread: Int,
                         highlighted: Bool) -> NSImage
{
    let image = NSImage(size: NSSize(width: 50, height: 22), flipped: false) { _ in
        let color: NSColor = highlighted ? .white : .labelColor
        let pot = potIcon(cooking: cooking, phase: phase, waiting: waiting, color: color)
        pot.isTemplate = false
        pot.draw(in: NSRect(x: 0, y: 0, width: 22, height: 22))
        if unread > 0 {
            let rect = NSRect(x: 25, y: 4, width: 24, height: 14)
            NSColor.controlAccentColor.withAlphaComponent(highlighted ? 0.4 : 0.16).setFill()
            NSBezierPath(roundedRect: rect, xRadius: 7, yRadius: 7).fill()
            let text = unread > 9 ? "9+" : String(unread)
            let attributes: [NSAttributedString.Key: Any] = [
                .font: NSFont.monospacedDigitSystemFont(ofSize: 10, weight: .medium),
                .foregroundColor: highlighted ? NSColor.white : NSColor.labelColor,
            ]
            let size = (text as NSString).size(withAttributes: attributes)
            (text as NSString).draw(
                at: NSPoint(x: rect.midX - size.width / 2, y: rect.midY - size.height / 2),
                withAttributes: attributes
            )
        }
        if failed {
            color.setFill()
            ("!" as NSString).draw(
                at: NSPoint(x: 18, y: 1),
                withAttributes: [.font: NSFont.systemFont(ofSize: 9, weight: .bold), .foregroundColor: color]
            )
        }
        return true
    }
    image.isTemplate = false
    return image
}

/// Canonical Klee-inspired companion: tilted open rim, one eye, curling smile and two feet.
/// App exports, navigation and template menu-bar icons all render this vector master.
@MainActor func companionIcon(blink: CGFloat = 1, smile: CGFloat = 0,
                              steam: CGFloat = 0, phase: Double = 0.5, monochrome: NSColor? = nil) -> NSImage
{
    let image = NSImage(size: NSSize(width: 32, height: 32), flipped: false) { _ in
        NSGraphicsContext.saveGraphicsState()
        defer { NSGraphicsContext.restoreGraphicsState() }
        let transform = NSAffineTransform()
        transform.translateX(by: 0, yBy: 32)
        transform.scaleX(by: 1, yBy: -1)
        transform.concat()
        let ochre = monochrome ?? NSColor(srgbRed: 0.81, green: 0.59, blue: 0.18, alpha: 1)
        let ink = NSColor(srgbRed: 0.18, green: 0.17, blue: 0.14, alpha: 1)
        let body = NSBezierPath()
        body.move(to: NSPoint(x: 7.3, y: 8.1))
        body.curve(
            to: NSPoint(x: 24.5, y: 3.2),
            controlPoint1: NSPoint(x: 10, y: 5.3),
            controlPoint2: NSPoint(x: 23.5, y: 1.5)
        )
        body.curve(
            to: NSPoint(x: 23.1, y: 11.7),
            controlPoint1: NSPoint(x: 26, y: 3.5),
            controlPoint2: NSPoint(x: 21.4, y: 7.4)
        )
        body.curve(
            to: NSPoint(x: 26, y: 20),
            controlPoint1: NSPoint(x: 24, y: 15),
            controlPoint2: NSPoint(x: 27, y: 17)
        )
        body.curve(
            to: NSPoint(x: 20, y: 26.6),
            controlPoint1: NSPoint(x: 25.8, y: 23.5),
            controlPoint2: NSPoint(x: 23.2, y: 25.6)
        )
        body.curve(
            to: NSPoint(x: 21.2, y: 29.1),
            controlPoint1: NSPoint(x: 19.4, y: 28),
            controlPoint2: NSPoint(x: 20.5, y: 28.5)
        )
        body.curve(
            to: NSPoint(x: 17.1, y: 29.7),
            controlPoint1: NSPoint(x: 23, y: 30.3),
            controlPoint2: NSPoint(x: 18.1, y: 30.1)
        )
        body.line(to: NSPoint(x: 16.8, y: 26.7))
        body.line(to: NSPoint(x: 13.6, y: 26.7))
        body.curve(
            to: NSPoint(x: 12.6, y: 29.5),
            controlPoint1: NSPoint(x: 13.4, y: 28),
            controlPoint2: NSPoint(x: 13.5, y: 29)
        )
        body.curve(
            to: NSPoint(x: 9.4, y: 29.2),
            controlPoint1: NSPoint(x: 11.8, y: 30.1),
            controlPoint2: NSPoint(x: 7, y: 30.2)
        )
        body.curve(
            to: NSPoint(x: 10.7, y: 26.4),
            controlPoint1: NSPoint(x: 10.5, y: 28.6),
            controlPoint2: NSPoint(x: 11, y: 27.4)
        )
        body.curve(
            to: NSPoint(x: 5.5, y: 21.1),
            controlPoint1: NSPoint(x: 7.2, y: 25.9),
            controlPoint2: NSPoint(x: 5.1, y: 24.4)
        )
        body.curve(
            to: NSPoint(x: 8, y: 13.5),
            controlPoint1: NSPoint(x: 5, y: 17.7),
            controlPoint2: NSPoint(x: 7.9, y: 15.8)
        )
        body.curve(
            to: NSPoint(x: 7.3, y: 8.1),
            controlPoint1: NSPoint(x: 8.7, y: 11.1),
            controlPoint2: NSPoint(x: 6.5, y: 9.8)
        )
        body.close()
        ochre.setFill(); body.fill()
        // Clear the opening and template features, rather than painting a background color.
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current?.compositingOperation = .copy
        NSColor.clear.setFill()
        let opening = NSBezierPath()
        opening.move(to: NSPoint(x: 8.6, y: 8.3))
        opening.curve(
            to: NSPoint(x: 23.6, y: 3.6),
            controlPoint1: NSPoint(x: 11.8, y: 5.7),
            controlPoint2: NSPoint(x: 23.2, y: 2.2)
        )
        opening.curve(
            to: NSPoint(x: 8.6, y: 8.3),
            controlPoint1: NSPoint(x: 24.8, y: 4.9),
            controlPoint2: NSPoint(x: 10.8, y: 10.3)
        )
        opening.close(); opening.fill()
        NSGraphicsContext.restoreGraphicsState()
        NSGraphicsContext.saveGraphicsState()
        if monochrome != nil {
            NSGraphicsContext.current?.compositingOperation = .copy
        }
        (monochrome == nil ? ink : NSColor.clear).setFill()
        let eyeHeight = max(0.55, 1.65 * blink)
        NSBezierPath(ovalIn: NSRect(x: 20, y: 11.6 - eyeHeight / 2, width: 1.65, height: eyeHeight)).fill()
        (monochrome == nil ? ink : NSColor.clear).setStroke()
        let face = NSBezierPath()
        face.move(to: NSPoint(x: 22, y: 14.1))
        face.curve(
            to: NSPoint(x: 20.7, y: 20),
            controlPoint1: NSPoint(x: 22, y: 16.6),
            controlPoint2: NSPoint(x: 21.7, y: 18.9)
        )
        if monochrome == nil {
            face.curve(
                to: NSPoint(x: 22.1, y: 21.5),
                controlPoint1: NSPoint(x: 23, y: 19.1),
                controlPoint2: NSPoint(x: 23.5, y: 20.8)
            )
        }
        face.curve(
            to: NSPoint(x: 10, y: 20.8 - smile),
            controlPoint1: NSPoint(x: 19.6, y: 24 + smile),
            controlPoint2: NSPoint(x: 13.5, y: 23.5 + smile)
        )
        face.lineWidth = monochrome == nil ? 0.65 : 1.15
        face.lineCapStyle = .round; face.lineJoinStyle = .round; face.stroke()
        NSGraphicsContext.restoreGraphicsState()
        if steam > 0 {
            ochre.withAlphaComponent(steam * 0.8).setStroke()
            for (index, x) in [12.0, 16.0].enumerated() {
                let drift = sin(phase * .pi * 2 + Double(index)) * 0.5
                let line = NSBezierPath(); line.move(to: NSPoint(x: x, y: 4))
                line.curve(to: NSPoint(x: x + drift, y: 0.6),
                           controlPoint1: NSPoint(x: x - 0.8 + drift, y: 2.8),
                           controlPoint2: NSPoint(x: x + 0.8 + drift, y: 1.7))
                line.lineWidth = 1; line.lineCapStyle = .round; line.stroke()
            }
        }
        return true
    }
    image.isTemplate = monochrome != nil
    return image
}
