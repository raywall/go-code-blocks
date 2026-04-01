.PHONY: run test coverage

# Captura o segundo argumento da linha de comando (ex: auth, agent, upload)
SAMPLE := $(word 2, $(MAKECMDGOALS))

run:
	@go run samples/$(SAMPLE)/main.go

test:
	@go test ./...; \
	 act -j test --container-architecture linux/arm64;

coverage:
	@go test -coverprofile=coverage.out ./...; \
	 go tool cover -html=coverage.out -o coverage.html;

# Evita que o Make retorne erro tentando executar o argumento passado no 'run'
%:
	@: