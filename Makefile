# Default target
.PHONY: help
help:
	@echo "Available commands:"
	@echo "  make dev                  - Run all dev watchers (templ, tailwind, server)"
	@echo "  make build                - Build static pure-Go binary for production"
	@echo "  make generate             - Generate all code (templ, sqlc, tailwind)"
	@echo "  make vet                  - Run go vet ./..."
	@echo "  make test                 - Run race-enabled test suite"
	@echo "  make clean                - Clean generated files and build artifacts"
	@echo "  make install_dependencies - Install all development dependencies"

# Install all development dependencies
.PHONY: install_dependencies
install_dependencies:
	@echo "Installing Go development tools..."
	@go install github.com/a-h/templ/cmd/templ@v0.3.977 \
		&& echo "✅ Templ installed successfully" \
		|| (echo "❌ Templ installation failed" && exit 1)

	@go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 \
		&& echo "✅ sqlc installed successfully" \
		|| (echo "❌ sqlc installation failed" && exit 1)

	@echo "Installing Node.js dependencies..."
	@npm ci \
		&& echo "✅ Node dependencies installed" \
		|| (echo "❌ npm ci failed" && exit 1)

	@echo "🎉 All dependencies installed!"

# Run all dev watchers
.PHONY: dev
dev:
	@echo "Running initial code generation..."
	@make --no-print-directory generate
	@echo ""
	@echo "Starting development watchers..."
	@make --no-print-directory -j2 dev-templ dev-tailwind

# Watch Templ files AND Run Server
.PHONY: dev-templ
dev-templ:
	templ generate --watch --proxy="http://localhost:8080" --cmd="go run ./cmd/server"

# Watch CSS (Tailwind v4)
.PHONY: dev-tailwind
dev-tailwind:
	npx @tailwindcss/cli -i ./web/static/css/input.css -o ./web/static/css/output.css --watch

# Generate all code (useful after git pull or initial setup)
.PHONY: generate
generate:
	@echo "Generating templ files..."
	@templ generate
	@echo "Generating sqlc files..."
	@sqlc generate
	@echo "Generating CSS..."
	@npx @tailwindcss/cli -i ./web/static/css/input.css -o ./web/static/css/output.css --minify
	@echo "Done!"

# Build static pure-Go binary for production (CGO_ENABLED=0)
.PHONY: build
build:
	@echo "Building pure-Go binary for production..."
	@templ generate
	@npx @tailwindcss/cli -i web/static/css/input.css -o web/static/css/output.css --minify
	@CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/wledger ./cmd/server
	@echo "Build complete: bin/wledger"

# Run go vet
.PHONY: vet
vet:
	go vet ./...

# Run tests
.PHONY: test
test:
	go test -race -count=1 ./...

# Clean generated files
.PHONY: clean
clean:
	@echo "Cleaning generated files..."
	@rm -rf tmp/
	@rm -rf bin/
	@rm -f web/static/css/output.css
	@find . -name "*_templ.go" -type f -delete
	@echo "Clean complete!"
