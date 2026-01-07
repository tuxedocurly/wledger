# Default target
.PHONY: help
help:
	@echo "Available commands:"
	@echo "  make dev        - Run all dev watchers (templ, tailwind, server)"
	@echo "  make build      - Build for production"
	@echo "  make generate   - Generate all code (templ, sqlc, tailwind)"
	@echo "  make clean      - Clean generated files and build artifacts"
	@echo "  make test       - Run tests"
	@echo "  make install_dependencies - Install all development dependencies"

# Install all development dependencies
.PHONY: install_dependencies
install_dependencies:
	@echo "Installing Go development tools..."
	@go install github.com/a-h/templ/cmd/templ@latest \
		&& echo "✅ Templ installed successfully" \
		|| (echo "❌ Templ installation failed" && exit 1)
		
	@go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest \
		&& echo "✅ sqlc installed successfully" \
		|| (echo "❌ sqlc installation failed" && exit 1)

	@echo "Installing Node.js dependencies..."
	@npm install \
		&& echo "✅ Node dependencies installed" \
		|| (echo "❌ npm install failed" && exit 1)
		
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
# Using --cmd to run the server directly after generation
.PHONY: dev-templ
dev-templ:
	templ generate --watch --proxy="http://localhost:8080" --cmd="go run -tags fts5 ./cmd/server"

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
	@npx @tailwindcss/cli -i ./web/static/css/input.css -o ./web/static/css/output.css
	@echo "Done!"

# Build for production
.PHONY: build
build:
	@echo "Building for production..."
	@templ generate
	@npx @tailwindcss/cli -i web/static/css/input.css -o web/static/css/output.css --minify
	@go build -tags fts5 -o bin/wledger ./cmd/server
	@echo "Build complete: bin/wledger"

# Run tests
.PHONY: test
test:
	go test -v -tags fts5 ./...

# Clean generated files
.PHONY: clean
clean:
	@echo "Cleaning generated files..."
	@rm -rf tmp/
	@rm -rf bin/
	@rm -f web/static/css/output.css
	@find . -name "*_templ.go" -type f -delete
	@echo "Clean complete!"
