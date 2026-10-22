import CoreGraphics
import Foundation
import ImageIO
import Testing
import UniformTypeIdentifiers
@testable import encore_bridge

/// A w x h PNG, red on the left half and blue on the right.
private func sourcePNG(width: Int, height: Int) -> Data {
  let space = CGColorSpace(name: CGColorSpace.sRGB)!
  let ctx = CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0, space: space,
                      bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
  ctx.setFillColor(CGColor(srgbRed: 1, green: 0, blue: 0, alpha: 1))
  ctx.fill(CGRect(x: 0, y: 0, width: width / 2, height: height))
  ctx.setFillColor(CGColor(srgbRed: 0, green: 0, blue: 1, alpha: 1))
  ctx.fill(CGRect(x: width / 2, y: 0, width: width - width / 2, height: height))
  let data = NSMutableData()
  let dest = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil)!
  CGImageDestinationAddImage(dest, ctx.makeImage()!, nil)
  CGImageDestinationFinalize(dest)
  return data as Data
}

@Test func `covers are written as square JPEGs`() throws {
  let url = URL(fileURLWithPath: NSTemporaryDirectory() + "art-image-\(UUID().uuidString).jpg")
  defer { try? FileManager.default.removeItem(at: url) }

  let (w, h) = try ArtImage.writeSquareJPEG(sourcePNG(width: 600, height: 400), size: 256, to: url)
  #expect(w == 256 && h == 256)

  let bytes = try Data(contentsOf: url)
  #expect(bytes.prefix(3) == Data([0xFF, 0xD8, 0xFF])) // JPEG start of image
  let src = try #require(CGImageSourceCreateWithData(bytes as CFData, nil))
  #expect(CGImageSourceGetType(src) as String? == UTType.jpeg.identifier)
  let image = try #require(CGImageSourceCreateImageAtIndex(src, 0, nil))
  #expect(image.width == 256 && image.height == 256)
  #expect(bytes.count < 20_000) // two flat colours; PNG of a photo is 100 KB
}

@Test func `a cover is never scaled up`() throws {
  let url = URL(fileURLWithPath: NSTemporaryDirectory() + "art-image-\(UUID().uuidString).jpg")
  defer { try? FileManager.default.removeItem(at: url) }
  let (w, h) = try ArtImage.writeSquareJPEG(sourcePNG(width: 100, height: 120), size: 600, to: url)
  #expect(w == 100 && h == 100)
}

@Test func `data that is not an image throws`() {
  let url = URL(fileURLWithPath: NSTemporaryDirectory() + "art-image-\(UUID().uuidString).jpg")
  #expect(throws: (any Error).self) {
    try ArtImage.writeSquareJPEG(Data("nope".utf8), size: 64, to: url)
  }
  #expect(!FileManager.default.fileExists(atPath: url.path))
}
