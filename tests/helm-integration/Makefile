.PHONY: help test test-verbose test-single clean docs lint fmt

help:
	@echo "Helm Integration Tests (using kind)"
	@echo ""
	@echo "Prerequisites:"
	@echo "  - kind (Kubernetes in Docker)"
	@echo "  - helm"
	@echo "  - docker"
	@echo ""
	@echo "Available targets:"
	@echo "  make test              - Run all helm integration tests"
	@echo "  make clean             - Clean up all resources"
	@echo "  make lint              - Lint code with ruff and mypy"
	@echo "  make fmt               - Format code with ruff"
	@echo "  make docs              - Show detailed documentation"

test:
	@echo ""
	@echo "Running helm integration tests..."
	@echo "==========================================="
	@echo ""
	uv run pytest -vv --tb=long -s

clean:
	@echo "Cleaning up..."
	rm -rf .venv .pytest_cache __pycache__
	@echo "✓ Cleanup complete"

lint:
	@echo "Running ruff linter..."
	uv run ruff check .
	@echo "Running mypy type checker..."
	uv run mypy .

fmt:
	@echo "Formatting code with ruff..."
	uv run ruff format .

docs:
	@cat README.md
