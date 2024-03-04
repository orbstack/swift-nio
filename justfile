run:
	cargo build
	codesign --entitlements res/entitlements.xml -s "${CERT_NAME}" target/debug/libkrun
	RUST_LOG="info" DYLD_LIBRARY_PATH="res" ./target/debug/libkrun
