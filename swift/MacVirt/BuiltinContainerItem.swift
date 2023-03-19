//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct BuiltinContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var record: ContainerRecord

    @State private var actionInProgress = false

    var body: some View {
        HStack {
            Image("distro_\(record.image.distro)")
                    .resizable()
                    .interpolation(.high)
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 32, height: 32)
                    .padding(.trailing, 8)

            VStack(alignment: .leading) {
                Text("Docker")
                        .font(.headline)
                Text("Build and run Docker containers. [Learn more](https://docs.docker.com/get-started/overview/#containers)")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
            }
            Spacer()

            let binding = Binding<Bool>(
                get: { record.running },
                set: { newValue in
                    Task { @MainActor in
                        actionInProgress = true
                        if newValue {
                            await vmModel.tryStartContainer(record)
                        } else {
                            await vmModel.tryStopContainer(record)
                            // delete stale data
                            // cause reload next time
                            vmModel.dockerContainers = nil
                        }
                        actionInProgress = false
                    }
                }
            )
            Toggle(isOn: binding) {
            }
            .toggleStyle(SwitchToggleStyle(tint: .accentColor))
            .disabled(actionInProgress)
        }
        .padding(.vertical, 4)
    }
}