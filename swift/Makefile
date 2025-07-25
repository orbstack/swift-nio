all: swift

.PHONY: swift
swift:
	xcodebuild -workspace MacVirt.xcodeproj/project.xcworkspace/ -scheme MacVirt -configuration Debug build

.PHONY: run
run: swift
	./DerivedData/MacVirt/Build/Products/Debug/OrbStack.app/Contents/MacOS/OrbStack
