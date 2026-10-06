// macOS export helper: swift -module-cache-path /tmp/cads-swift-cache export_figures.swift DIRECTORY
import AppKit
import Foundation

final class FigureView: NSView {
    let image: NSImage
    init(image: NSImage) {
        self.image = image
        super.init(frame: NSRect(origin: .zero, size: image.size))
    }
    required init?(coder: NSCoder) { fatalError("Not used") }
    override func draw(_ dirtyRect: NSRect) {
        NSColor.white.setFill()
        bounds.fill()
        image.draw(in: bounds)
    }
}
let directory = URL(fileURLWithPath: CommandLine.arguments[1])
let files = try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
for url in files.filter({$0.pathExtension == "svg"}).sorted(by: {$0.path < $1.path}) {
    guard let source = NSImage(contentsOf: url) else { fatalError("Cannot load \(url.path)") }
    let size = source.size
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(size.width * 2), pixelsHigh: Int(size.height * 2), bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    bitmap.size = size
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    NSColor.white.setFill()
    NSRect(origin: .zero, size: size).fill()
    source.draw(in: NSRect(origin: .zero, size: size))
    NSGraphicsContext.restoreGraphicsState()
    let base = url.deletingPathExtension()
    try bitmap.representation(using: .png, properties: [:])!.write(to: base.appendingPathExtension("png"))
    let view = FigureView(image: source)
    try view.dataWithPDF(inside: view.bounds).write(to: base.appendingPathExtension("pdf"))
    print("Exported \(base.lastPathComponent): PNG + PDF")
}
