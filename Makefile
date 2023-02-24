.PHONY: app server publish pub

app:
	@./build-app.sh

server:
	@cd updates
	@python3 -m http.server

serve: server

publish:
	@./publish-update.sh

pub: publish
