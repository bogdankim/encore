import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

enum ArtImage {
  struct DecodeError: Error, CustomStringConvertible {
    var description: String { "artwork data is not a decodable image" }
  }

  /// JPEG quality of the covers written to the cache. A 256 px cover is about
  /// 10 KB at this quality against 97 KB as PNG, and decodes several times faster.
  static let jpegQuality = 0.9

  /// Decodes `data`, center-crops it to a square, scales it to `size` px (never
  /// up) and writes it as JPEG to `url` atomically. Returns the output dimensions.
  static func writeSquareJPEG(_ data: Data, size: Int, to url: URL) throws -> (Int, Int) {
    guard let src = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary),
          let image = CGImageSourceCreateImageAtIndex(src, 0, nil) else { throw DecodeError() }
    let side = min(image.width, image.height)
    let crop = CGRect(x: (image.width - side) / 2, y: (image.height - side) / 2, width: side, height: side)
    guard let square = image.cropping(to: crop) else { throw DecodeError() }
    let out = min(size, side)

    let space = CGColorSpace(name: CGColorSpace.sRGB)!
    guard let ctx = CGContext(data: nil, width: out, height: out, bitsPerComponent: 8, bytesPerRow: 0, space: space,
                              bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue) else { throw DecodeError() }
    ctx.interpolationQuality = .high
    ctx.draw(square, in: CGRect(x: 0, y: 0, width: out, height: out))
    guard let scaled = ctx.makeImage() else { throw DecodeError() }

    let jpeg = NSMutableData()
    guard let dest = CGImageDestinationCreateWithData(jpeg, UTType.jpeg.identifier as CFString, 1, nil) else { throw DecodeError() }
    let options = [kCGImageDestinationLossyCompressionQuality: jpegQuality] as CFDictionary
    CGImageDestinationAddImage(dest, scaled, options)
    guard CGImageDestinationFinalize(dest) else { throw DecodeError() }

    try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
    try (jpeg as Data).write(to: url, options: .atomic)
    return (out, out)
  }
}
