.PHONY: test build run up down logs e2e e2e-multiproject e2e-redis

test:
	go test ./...

build:
	go build ./cmd/dbbroker

run:
	go run ./cmd/dbbroker

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f broker postgres redis

e2e:
	./scripts/e2e.sh

e2e-multiproject:
	./scripts/e2e-multiproject.sh

e2e-redis:
	./scripts/e2e-redis.sh
