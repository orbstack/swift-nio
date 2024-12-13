import Foundation

@_cdecl("swext_files_get_container_dir")
func swext_files_get_container_dir() -> UnsafeMutablePointer<CChar> {
    let url = FileManager.default.containerURL(
        forSecurityApplicationGroupIdentifier: "HUAQ24HBR6.dev.orbstack")
    return strdup(url?.path ?? "")
}
