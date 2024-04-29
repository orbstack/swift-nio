.PHONY: x86-build x86-test

x86-test: x86-build
	scp target/x86_64-apple-darwin/debug/kruntest mini:/tmp/kruntest
	ssh mini /tmp/kruntest

x86-build:
	cargo build --target x86_64-apple-darwin
	codesign -f --entitlements kruntest.entitlements -s - target/x86_64-apple-darwin/debug/kruntest
