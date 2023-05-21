//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct BuiltinContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var record: ContainerRecord

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(machine: record) != nil

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
                get: { record.state == .starting || record.running },
                set: { newValue in
                    Task { @MainActor in
                        if newValue {
                            await actionTracker.with(machine: record, action: .start) {
                                await vmModel.tryStartContainer(record)
                            }
                        } else {
                            await actionTracker.with(machine: record, action: .stop) {
                                await vmModel.tryStopContainer(record)
                            }

                            // delete stale data
                            // cause reload next time
                            vmModel.dockerContainers = nil
                        }
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