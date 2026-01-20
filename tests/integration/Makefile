.PHONY: help test lint fmt

help:
	@echo "Integration tests makefile targets"
	@echo "  make test              - Run moto integration tests"
	@echo "  make lint              - Lint code with ruff and mypy"
	@echo "  make fmt               - Format code with ruff"

test:
	@echo "Running moto integration tests..."
	uv run pytest . -v -n auto

lint:
	@echo "Running ruff linter..."
	uv run ruff check .
	@echo "Running mypy type checker..."
	uv run mypy .

fmt:
	@echo "Formatting code with ruff..."
	uv run ruff format .
