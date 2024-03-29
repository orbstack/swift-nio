build:
	cargo build
	codesign -f --entitlements res/entitlements.xml -s - target/debug/libkrun
	codesign -f --entitlements res/entitlements.xml -s - target/debug/deps/libkrun-9ddcb39b8dbdf04f

run:
	just build
	source private/.env && RUST_BACKTRACE=1 RUST_LOG="info,gicv3=trace" ./target/debug/libkrun

run-real:
	just build
	source private/.env && RUST_BACKTRACE=1 RUST_LOG="info" ./target/debug/libkrun

dbg:
	just build
	sudo rust-lldb target/debug/deps/libkrun-9ddcb39b8dbdf04f
