# Documents for both humans and coding agents

* [README.md](./README.md)

# Documents for coding agents

* [./.agent/docs/DESIGN.md](./.agent/docs/DESIGN.md) ... design documents
* [./.agent/docs/IMPLEMENTATION.md](./.agent/docs/IMPLEMENTATION.md) ... implementation notes and workplan.
* [./.agent/docs/PEER_REVIEW.md](./.agent/docs/PEER_REVIEW.md) ... peer code review history.

# Rules and protocols

## File Management

* When you'd make summary documents for your work, be sure to write them under `./.agent/docs`, not under `/tmp`.
* Temporary files should be created under `./.agent/tmp`, not under `/tmp`.
* ❌ Do not randomly create a binary under the version controlled directory through `go build ./cmd/s3router`. Always put it under `./.agent/tmp`.
* ❌ Never delete user files without permission. Only safe to delete: files YOU created in THIS session that are in `./.agent/tmp/`. Always ask first if unsure. Assume all pre-existing files belong to user.

## Documentation

* Try to write your work summary to one of the existing documents.
* ❌ Avoid editing any existing sections of PEER_REVIEW.md. You should rather just append texts to it.

## Testing

* Make sure that regression tests are ready for your fix.
* Use `github.com/stretchr/testify/assert` for Go code unit tests, and use pytest for integration tests.
* ❌ You shouldn't run the entire integration test suites at once. Or if you can spare them 2+ minutes, be patient with it. You should always specify `--maxfail=n` (n should be a number less than 10), and also be sure to specify `--lf` as well when you want to run the last failing tests.

## Python

* If there's a `pyproject.toml` file, try to run the tests with `uv run pytest ...` and arbitrary scripts with `uv run python ...`.
  * ❌ If there's no `pyproject.toml`, never run a bare `pip install` out of a venv. Always use `uv pip ...` in combination with `uv venv`.
* For typed Python tests:
  * Use `types-boto3[s3]` and import `S3Client` from `types_boto3_s3` for boto3 clients.
  * Keep fixture return types as `Iterator[Type]` for yield fixtures.
  * Avoid `dict[str, object]` and `list[object]` in annotations; use `dict[str, Any]`/`list[Any]` for mixed payloads and real TypedDicts for structured shapes.
  * Disambiguate response variables when mixing `get_object`/`head_object`/`put_object` to avoid TypedDict assignment conflicts.
  * Prefer `types_boto3_s3.type_defs` for request/response TypedDicts (e.g., `CompletedPartTypeDef`, `ObjectIdentifierTypeDef`).

## Git Workflow

* ❌ Don't resort to either `git checkout ...` or `git restore ...` relentlessly. Someone (or another coding agent) may have touched the files. Also, your past changes haven't been committed as you expected them to be.
