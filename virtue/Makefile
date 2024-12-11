.PHONY: x86 x86-test arm-test arm test-gruel

x86-test: x86
	ssh mini rm -f /tmp/vtest
	scp target/x86_64-apple-darwin/debug/vtest mini:/tmp/vtest
	ssh -t mini RUST_LOG=info RUST_BACKTRACE=1 /tmp/vtest

x86:
	cargo build --target x86_64-apple-darwin
	codesign -f --entitlements vtest.entitlements -s - target/x86_64-apple-darwin/debug/vtest

arm-test: arm
	ssh air rm -f /tmp/vtest
	scp target/debug/vtest air:/tmp/vtest
	ssh -t air RUST_LOG=info RUST_BACKTRACE=1 /tmp/vtest

arm:
	cargo build
	codesign -f --entitlements vtest.entitlements -s - target/debug/vtest

test-gruel:
	cargo test -p gruel
	LOOM_MAX_PREEMPTIONS=5 RUSTFLAGS="--cfg loom" cargo test -p gruel --release
