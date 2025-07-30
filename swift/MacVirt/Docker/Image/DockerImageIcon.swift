import SwiftUI

private let boundaryRegex = try! Regex("\\W+")

private func getIconForImage(rawImageTag: String, env: [String]? = nil) -> String? {
    // TODO: smarter resolution based on image hash -> DKImage

    // strip version tag
    guard let image = rawImageTag.split(separator: ":").first else {
        return nil
    }

    // get last two parts of image name:
    let parts = image.split(separator: "/")
    guard parts.count >= 1 else {
        return nil
    }

    let name = String(parts[parts.count - 1])
    let org = parts.count >= 2 ? String(parts[parts.count - 2]) : nil

    // 1. prefer name
    if imageLibraryIcons.contains(name) {
        return "container_img_library/\(name)"
    }

    // 2. try org
    if let org = org, imageLibraryIcons.contains(org) {
        return "container_img_library/\(org)"
    }

    // 3. try regex: \b<name>\b for patterns like "drud/ddev-dbserver-mariadb-10.3:v1.21.4-powermail-v11-built"
    var regexParts = rawImageTag.split(separator: boundaryRegex)
    regexParts.reverse() // more specific parts come last (e.g. "maltokyo/docker-nginx-webdav")
    for part in regexParts {
        if imageLibraryIcons.contains(String(part)) {
            return "container_img_library/\(part)"
        }
    }

    // placeholder
    return nil
}

private func getIconForTags(rawImageTags: [String], env: [String]? = nil) -> String? {
    for rawImageTag in rawImageTags {
        if let image = getIconForImage(rawImageTag: rawImageTag, env: env) {
            return image
        }
    }
    return nil
}

struct DockerImageIcon: View {
    let rawImageTags: [String]
    let env: [String]? = nil

    @ViewBuilder private var placeholder: some View {
        // 28px
        let color = SystemColors.forString(rawImageTags.first ?? "unknown")
        Image(systemName: "shippingbox.fill")
            .resizable()
            .aspectRatio(contentMode: .fit)
            .frame(width: 16, height: 16)
            .padding(6)
            .foregroundColor(Color(hex: 0xFAFAFA))
            .background(Circle().fill(color))
            // rasterize so opacity works on it as one big image
            .drawingGroup(opaque: false)
    }

    var body: some View {
        if let image = getIconForTags(rawImageTags: rawImageTags) {
            Image(image)
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(width: 28, height: 28)
                // if it's a full rgb square image with solid bg (e.g. from github), it looks much nicer to add subtle rounded corners
                .clipShape(RoundedRectangle(cornerRadius: 4))
        } else {
            placeholder
        }
    }
}
