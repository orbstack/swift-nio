run:
	cargo build
	codesign --entitlements res/entitlements.xml -s - target/debug/libkrun
	RUST_LOG="info" DYLD_LIBRARY_PATH="res" ./target/debug/libkrun
