.PHONY: x86 x86-test arm-test arm

x86-test: x86
	ssh mini rm -f /tmp/kruntest
	scp target/x86_64-apple-darwin/debug/kruntest mini:/tmp/kruntest
	ssh -t mini RUST_LOG=info RUST_BACKTRACE=1 /tmp/kruntest

x86:
	cargo build --target x86_64-apple-darwin
	codesign -f --entitlements kruntest.entitlements -s - target/x86_64-apple-darwin/debug/kruntest

arm-test: arm
	ssh air rm -f /tmp/kruntest
	scp target/debug/kruntest air:/tmp/kruntest
	ssh -t air RUST_LOG=info RUST_BACKTRACE=1 /tmp/kruntest

arm:
	cargo build
	codesign -f --entitlements kruntest.entitlements -s - target/debug/kruntest
