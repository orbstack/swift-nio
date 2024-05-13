.PHONY: x86 x86-test

x86-test: x86
	ssh mini rm -f /tmp/kruntest
	scp target/x86_64-apple-darwin/debug/kruntest mini:/tmp/kruntest
	ssh -t mini RUST_LOG=info RUST_BACKTRACE=1 /tmp/kruntest

x86:
	cargo build --target x86_64-apple-darwin
	codesign -f --entitlements kruntest.entitlements -s - target/x86_64-apple-darwin/debug/kruntest
