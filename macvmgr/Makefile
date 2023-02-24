.PHONY: vmgr run scli app server serve publish pub

run: vmgr
	@orb shutdown -f || :
	@./macvmgr vmgr

vmgr:
	@./build.sh

scli:
	@cd ../scon
	@make scli



app:
	@../build-app.sh

server:
	@cd updates
	@python3 -m http.server

serve: server

publish:
	@./publish-update.sh

pub: publish
