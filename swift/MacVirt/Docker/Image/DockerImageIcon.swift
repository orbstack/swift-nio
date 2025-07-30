import SwiftUI

private let boundaryRegex = try! Regex("\\W+")

private enum IconSource {
    case asset(String)
    case url(String)
}

private func getIconForTag(rawTag: String) -> IconSource? {
    // TODO: smarter resolution based on image hash -> DKImage

    // strip version tag
    guard let image = rawTag.split(separator: ":").first else {
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
        return .asset("container_img_library/\(name)")
    }

    // 2. try regex: \b<name>\b for patterns like "drud/ddev-dbserver-mariadb-10.3:v1.21.4-powermail-v11-built"
    var regexParts = rawTag.split(separator: boundaryRegex)
    regexParts.reverse() // more specific parts come last (e.g. "maltokyo/docker-nginx-webdav")
    for part in regexParts {
        if imageLibraryIcons.contains(String(part)) {
            return .asset("container_img_library/\(part)")
        }
    }

    // 3. try org (for cases with -, which regex won't match)
    if let org = org, imageLibraryIcons.contains(org) {
        return .asset("container_img_library/\(org)")
    }

    // placeholder
    return nil
}

private func getIconForImage(image: DKSummaryAndFullImage) -> IconSource? {
    // try all tags
    if let tags = image.summary.repoTags {
        for tag in tags {
            if let icon = getIconForTag(rawTag: tag) {
                return icon
            }
        }
    }

    // try all digests (for dangling images that still have some tag info)
    if let digests = image.summary.repoDigests {
        for digest in digests {
            // split the @ part away
            let tag = digest.split(separator: "@").first.map(String.init) ?? digest
            if let icon = getIconForTag(rawTag: tag) {
                return icon
            }
        }
    }

    // if it's ghcr.io, use github profile picture
    if let tags = image.summary.repoTags {
        for tag in tags {
            if tag.starts(with: "ghcr.io/") {
                let parts = tag.split(separator: "/")
                if parts.count >= 2 {
                    let org = String(parts[1])
                    return .url("https://github.com/\(org).png")
                }
            }
        }
    }

    // node.js base image?
    if let env = image.full.config?.env {
        for e in env {
            if e.starts(with: "NODE_VERSION=") {
                let version = e.split(separator: "=").last.map(String.init) ?? ""
                return .asset("container_img_library/nodejs/\(version)")
            }
        }
    }

    return nil
}

struct DockerImageIconPlaceholder: View {
    let id: String

    var body: some View {
        // 28px
        let color = SystemColors.forString(id)
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
}

struct DockerImageIcon: View {
    let image: DKSummaryAndFullImage

    var body: some View {
        if let icon = getIconForImage(image: image) {
            switch icon {
            case .asset(let name):
                Image(name)
                .resizable()
                .aspectRatio(contentMode: .fit)
                .frame(width: 28, height: 28)
                // if it's a full rgb square image with solid bg (e.g. from github), it looks much nicer to add subtle rounded corners
                .clipShape(RoundedRectangle(cornerRadius: 4))
            case .url(let url):
                AsyncImage(url: URL(string: url)) { phase in
                    if let image = phase.image {
                        image.resizable()
                            .aspectRatio(contentMode: .fit)
                            .frame(width: 28, height: 28)
                            // if it's a full rgb square image with solid bg (e.g. from github), it looks much nicer to add subtle rounded corners
                            .clipShape(RoundedRectangle(cornerRadius: 4))
                    } else {
                        DockerImageIconPlaceholder(id: image.id)
                    }
                }
            }
        } else {
            DockerImageIconPlaceholder(id: image.id)
        }
    }
}
