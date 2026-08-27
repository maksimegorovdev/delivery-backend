compose-up:
	docker compose up -d --build
.PHONY: compose-up

compose-down:
	docker compose down
.PHONY: compose-down

sync:
	go work sync
.PHONY: sync