// https://stackoverflow.com/questions/39075043/how-to-convert-data-to-hex-string-in-swift
import Foundation

/// Extension for making base64 representations of `Data` safe for
/// transmitting via URL query parameters
extension Data {

    /// Instantiates data by decoding a base64url string into base64
    ///
    /// - Parameter string: A base64url encoded string
    init?(base64URLEncoded string: String) {
        self.init(base64Encoded: string.base64URLToBase64())
    }

    /// Encodes the string into a base64url safe representation
    ///
    /// - Returns: A string that is base64 encoded but made safe for passing
    ///            in as a query parameter into a URL string
    func base64URLEncodedString() -> String {
        return self.base64EncodedString().base64ToBase64URL()
    }

}

private extension String {
    func base64ToBase64URL() -> String {
        // Make base64 string safe for passing into URL query params
        let base64url = self.replacingOccurrences(of: "/", with: "_")
        .replacingOccurrences(of: "+", with: "-")
        .replacingOccurrences(of: "=", with: "")
        return base64url
    }

    func base64URLToBase64() -> String {
        // Return to base64 encoding
        var base64 = self.replacingOccurrences(of: "_", with: "/")
        .replacingOccurrences(of: "-", with: "+")
        // Add any necessary padding with `=`
        if base64.count % 4 != 0 {
            base64.append(String(repeating: "=", count: 4 - base64.count % 4))
        }
        return base64
    }
}