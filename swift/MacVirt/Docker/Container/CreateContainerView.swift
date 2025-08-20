//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

// min 2 chars, disallows hidden files (^.)
private let dockerRestrictedNamePattern =
    (try? NSRegularExpression(pattern: "^[a-zA-Z0-9][a-zA-Z0-9_.-]+$"))!

struct CreateContainerView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var image = ""
    @State private var name = ""
    @State private var subnet = ""
    @State private var enableIPv6 = false

    @State private var formIsPresented = false

    @Binding var isPresented: Bool

    var body: some View {
        NavigationStack {
            ImageSelector(image: $image)
                .navigationDestination(
                    isPresented: $formIsPresented,
                    destination: {
                        CreateForm {
                            Section("New Container") {
                                let nameBinding = Binding<String>(
                                    get: { name },
                                    set: {
                                        self.name = $0
                                    })

                                ValidatedTextField(
                                    "Name", text: nameBinding,
                                    validate: { value in
                                        // duplicate
                                        if vmModel.dockerNetworks?[value] != nil {
                                            return "Already exists"
                                        }

                                        // regex
                                        if dockerRestrictedNamePattern.firstMatch(
                                            in: value, options: [],
                                            range: NSRange(location: 0, length: value.utf16.count))
                                            == nil
                                        {
                                            return "Invalid name"
                                        }

                                        return nil
                                    })
                            }

                            Section("Advanced") {
                                Toggle("IPv6", isOn: $enableIPv6)

                                TextField(
                                    "Subnet (IPv4)", text: $subnet, prompt: Text("172.30.30.0/24"))
                            }

                            CreateButtonRow {
                                HelpButton {
                                    NSWorkspace.shared.open(
                                        URL(string: "https://orb.cx/docker-docs/container-create")!)
                                }

                                Button {
                                    isPresented = false
                                } label: {
                                    Text("Cancel")
                                }
                                .keyboardShortcut(.cancelAction)

                                CreateSubmitButton("Create")
                                    .keyboardShortcut(.defaultAction)
                            }
                        } onSubmit: {
                            formIsPresented = true
                        }.onAppear {
                            enableIPv6 = vmModel.dockerEnableIPv6
                        }
                        .navigationTitle("Container Settings")
                    })
        }
        .frame(minHeight: 400)
    }
}

private enum ImagesTab {
    case recentlyUsed
    case local

    case dockerHub
    case github
    case ecr
}

private struct ImageSelector: View {
    @Binding var image: String

    @State private var selectedTab = ImagesTab.dockerHub
    @State private var selectedImage: String? = nil
    @State private var searchField = ""

    @EnvironmentObject private var vmModel: VmViewModel

    var body: some View {
        NavigationSplitView {
            List(selection: $selectedTab) {
                Label("Recently Used", systemImage: "clock")
                    .tag(ImagesTab.recentlyUsed)
                Label("Local", systemImage: "folder")
                    .tag(ImagesTab.local)

                Section("Registry") {
                    Label("Docker Hub", systemImage: "house")
                        .tag(ImagesTab.dockerHub)
                    Label("GitHub", systemImage: "globe")
                        .tag(ImagesTab.github)
                    Label("ECR", systemImage: "cloud")
                        .tag(ImagesTab.ecr)
                }
            }
        } content: {
            switch selectedTab {
            case .recentlyUsed:
                Text("Recently Used")
            case .local:
                Text("Local")

            case .dockerHub:
                RegistryImageList(registry: "hub.docker.com", selectedImage: $selectedImage)
            case .github:
                RegistryImageList(registry: "ghcr.io", selectedImage: $selectedImage)
            case .ecr:
                RegistryImageList(registry: "public.ecr.aws", selectedImage: $selectedImage)
            }
        } detail: {
            if let image = selectedImage {
                RegistryImageDetail(image: image)
            } else {
                ContentUnavailableViewCompat("Select an image")
            }
        }
    }
}

private struct RegistryImageDetail: View {
    let image: String

    var body: some View {
        Text(image)
    }
}

private struct RegistryImageList: View {
    let registry: String
    @Binding var selectedImage: String?

    @StateObject private var model = RegistryImageModel()

    var body: some View {
        List(model.repositories, selection: $selectedImage) { repository in
            VStack(alignment: .leading) {
                Text(repository.name)
                    .font(.headline)
                Text(repository.description)
                    .font(.subheadline)
            }
        }
        .onAppear {
            model.fetchRepositories(registry: registry)
        }
    }
}

private class RegistryImageModel: ObservableObject {
    @Published var repositories: [DRRepository] = []

    func fetchRepositories(registry: String) {
        Task { @MainActor in
            do {
                let list = try await getRepositories(registry: registry)
                self.repositories = list.results
            } catch {
                print("Error fetching repositories: \(error)")
            }
        }
    }

    private func getRepositories(registry: String) async throws -> DRRepositoryList {
        let url: URL = URL(
            string: "https://\(registry)/v2/repositories/library/?page=1&page_size=100")!
        let (data, _) = try await URLSession.shared.data(from: url)

        print("data: \(String(data: data, encoding: .utf8)!)")

        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        // let iso8601DateFormatter = ISO8601DateFormatter()
        // iso8601DateFormatter.formatOptions = [.withFullDate, .withTime, .withFractionalSeconds]
        // decoder.dateDecodingStrategy = .formatted(iso8601DateFormatter)
        return try decoder.decode(DRRepositoryList.self, from: data)
    }
}
