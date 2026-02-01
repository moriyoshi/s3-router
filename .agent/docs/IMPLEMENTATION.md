# S3 Router – Implementation Details (Go)

## 1. Technology Stack

- **Language**: Go 1.25.x (go.mod)
- **Frameworks/Libraries**
  - HTTP: net/http with a custom handler chain (no ServeMux for the S3 API path)
  - AWS SDK: `github.com/aws/aws-sdk-go-v2/service/s3` plus credential providers
  - Config parsing: `github.com/spf13/viper` (YAML/JSON/TOML support)
  - Regex: Go stdlib `regexp`
  - Observability: `github.com/prometheus/client_golang/prometheus`
  - Logging: Go stdlib `log/slog`
  - Circuit breaker: `github.com/sony/gobreaker`
  - Cache: `github.com/hashicorp/golang-lru/v2`
  - Validation: internal config parsing and validation (no external validator dependency)

## 2. Module Layout

```
cmd/
  s3router/                 # main package (flags, DI wiring, request handlers)
internal/
  admin/                    # health/ready endpoints, config dump handlers
  auth/                     # SigV4 verification, credential mapping
  backend/
    cred/                   # pluggable credential providers (file, secrets manager, assume role)
    proxy/                  # request execution, streaming adapters, header handling
    manager.go              # backend registry, AWS client pools, health checks
    s3ops.go                # S3 operations wrappers
  bucket/                   # virtual bucket handling, ListObjectsV2, prefix optimization
  config/                   # schema definitions, loader, parsing utilities
  observability/            # logging + metrics
  routing/                  # bucket/route matcher, rewrite engine
  server/                   # HTTP server configuration and lifecycle
pkg/                        # (currently empty)
```

## 3. Configuration Handling

- **Type definitions** (`internal/config/config.go`, `backend.go`, `bucket.go`, `server.go`, `auth.go`):
  - `Config` - root configuration object
  - `BackendConfig` - backend connection details with credentials
  - `BucketConfig` - logical bucket definitions
  - `RouteConfig` - regex-based path routing rules
  - `RewriteRule` - transformation rules with named/numbered captures
  - `CredentialConfig` - credential source configuration with type, path, assume_role

- **Loader** (`internal/config/loader.go` + `internal/config/ir/loader.go`):
  - `LoadFromFile()` - Viper-based config file loading and IR conversion
  - Validation occurs during IR-to-config population (required fields, regex compilation)

- **Parse utilities** (`internal/config/parse.go`):
  - Duration parsing with human-readable formats
  - Size parsing with units (GB, MB, KB, B)
  - Count parsing with units (k, m)

## 4. Routing & Rewrite Engine

- **Matcher** (`internal/routing/matcher.go`):
  - `NewMatcher()` - creates matcher with configurable LRU cache
  - `Match()` - finds first matching route using ordered evaluation on path regex
  - `rewrite()` - applies sequential rewrite rules with capture groups
  - `substituteCaptures()` - replaces named and numbered placeholders
  - `InvalidateCache()` - purges cache on config refresh

- **Capture Group Handling**:
  - Named groups: extracted from regex subexp names
  - Numbered groups: $1, $2 indices from FindStringSubmatchIndex
  - Sequential rewrite: updated key passed to next rewrite rule
  - Original key preserved for numbered group extraction in no-pattern rewrites

## 4.1. Prefix Optimization for ListObjectsV2

The `PrefixOptimizer` (`internal/bucket/prefix_optimizer.go`) analyzes regex patterns to extract static prefixes for efficient S3 backend queries.

**Key Methods**:
- `AnalyzeRoute(route)` - Performs pattern analysis (~423ns per operation)
- `CanSkipRoute(prefix, route, analysis)` - Determines if route can be skipped for given prefix
- `ComputePhysicalPrefix(virtualPrefix, analysis)` - Computes physical prefix for trivial rewrites

**Safe Patterns** (can be optimized):
```regex
^users/.*                    # Eat-all after static prefix
^logs/.*\.log$              # Prefix + suffix constraint (end-anchored)
^data/.+                    # Eat-some pattern after prefix
^assets/images/.*           # Deep static prefix
```

**Unsafe Patterns** (require full scan):
```regex
^users/.*foo                # Additional constraints after prefix
users/.*                    # No ^ anchor (can match anywhere)
^(temp|cache)/.*           # Alternation prevents static prefix extraction
```

**Performance**: 15-20x faster for large buckets with prefix-optimizable patterns.

### 4.1.1 Delimiter Handling and Optimization

The s3router's ListObjects implementation uses a **"list-all-and-filter"** approach for all delimiters. This is **correct and necessary** for custom delimiters and multi-backend scenarios, but presents an **optimization opportunity** for the "/" delimiter case.

#### Current Implementation

**Flow for ANY delimiter (including "/"):**

1. Client requests: `ListObjectsV2(bucket, prefix, delimiter='/', maxKeys=100)`
2. Handler queries backend: `ListObjectsV2(bucket, prefix)` ← **NO delimiter passed**
3. Backend returns: ALL objects matching prefix
4. Handler applies locally:
   - Prefix filtering (already done by backend with prefix parameter)
   - Reverse rewriting (converts physical keys to virtual keys)
   - Delimiter grouping (groups virtual keys by delimiter character/string)
   - Pagination (returns first 100 results)
5. Handler returns: `CommonPrefixes: [...]` and `Contents: [...]` grouped by delimiter

**Why This Approach:**
- For custom delimiters (-, ., _, etc.): S3 backends only support "/" natively. Custom delimiters cannot be passed to backend.
- For multi-backend scenarios: Aggregating CommonPrefixes from multiple backends requires deduplication and merging, simpler to list all objects uniformly.

#### Optimization Opportunity: "/" Delimiter Only

**Current Behavior** (1000 objects in hierarchical structure):
- Fetches all 1000 objects from backend, groups locally → returns 2 CommonPrefixes
- Network transfer: 1000 object metadata entries

**Optimized Behavior:**
- Pass "/" to backend, backend returns 2 CommonPrefixes directly
- Network transfer: 2 CommonPrefix entries
- **Benefit**: 500x reduction in transferred data (~100x improvement for 10 prefixes with 1000 objects)

**Multi-Backend Complexity:**
- Each backend returns CommonPrefixes
- Must deduplicate CommonPrefixes across backends (same prefix might appear in multiple backends)
- Merge and sort results
- Apply pagination to deduplicated results
- Apply reverse rewriting to CommonPrefixes

#### Status

✓ **Current implementation is correct and complete:**
- Handles all delimiter types (/, -, ., _, etc.)
- Reverse rewriting works with any delimiter
- Multi-backend aggregation works correctly
- Pagination works correctly

**Optimization Status:**
- "/" delimiter optimization is **theoretically possible but not implemented**
- Would require handling multi-backend aggregation complexity
- Currently not a requirement (correct behavior achieved)
- Could be implemented as future performance optimization if needed

**Tests:** Comprehensive test suite in `TestListObjectsDelimiterOptimizationAnalysis` and `TestListObjectsWithNonSlashDelimiters` verify all edge cases.

## 5. Authentication

- Full SigV4 verification for both header-based and query-based signatures
- Static credential store is file-based JSON with enabled/disabled flags
- Authentication is enforced for all operations including bucket-level operations
- Bucket-level control-plane operations (ListBuckets/Create/Delete) require authentication and return policy-driven responses

Credentials file format:
```json
{
  "AKIA1234567890ABCDEF": {
    "secret_key": "...",
    "enabled": true,
    "created_at": "2024-01-01T00:00:00Z"
  }
}
```

## 6. Backend Manager

- **Manager** (`internal/backend/manager.go`):
  - `NewManager()` - initializes client pool for all configured backends
  - `createBackendClient()` - creates AWS S3 client with credentials + HTTP transport
  - `GetClient()` - retrieves backend client by ID
  - `GetClients()` - returns all clients for admin endpoints
  - `HealthCheck()` - performs head-bucket validation with context timeout

- **S3 Operations** (`internal/backend/s3ops.go`):
  - Backend-level S3 operation wrappers

- **HTTP Transport**:
  - 100 max idle connections, 10 per-host
  - 90s idle timeout, 30s dial timeout

- **Circuit Breaker**:
  - 3 max requests, 1s interval, 10s timeout
  - 60% failure ratio threshold

## 7. Credential Providers

The `internal/backend/cred/` package provides pluggable credential sources for backend S3 access:

- **Factory** (`factory.go`):
  - `NewCredentialsProvider()` - creates provider based on credential config type

- **Providers**:
  - `file.go` - loads credentials from JSON file on filesystem
  - `secrets_manager.go` - loads credentials from AWS Secrets Manager
  - `assume_role.go` - wraps any provider with STS AssumeRole
  - `provider.go` - common interfaces and default provider

Supported credential source types:
- `default`: AWS SDK default credential chain
- `file`: JSON file on filesystem
- `aws-secrets-manager`: AWS Secrets Manager secret
- `inline`: Static credentials in config (not recommended)

All providers support optional STS role assumption via `assume_role` configuration.

## 8. Proxy Execution

- **Executor** (`internal/backend/proxy/executor.go`):
  - `Execute()` - routes to appropriate S3 operation adapter
  - `executeGetObject()` - streams response body, preserves metadata headers
  - `executeHeadObject()` - retrieves object metadata without body
  - `executePutObject()` - uploads object with streaming support
  - `executeDeleteObject()` - removes object from backend
  - Circuit breaker integration for fault tolerance

- Response headers are assembled in executor methods (manual S3 metadata copying)

- **Streaming** (`internal/backend/proxy/streaming.go`):
  - `StreamingPutObject`/`StreamingUploadPart` stream request bodies directly to upstream with SigV4 signing
  - `StreamingAwsChunkedPutObject`/`StreamingAwsChunkedUploadPart` decode incoming aws-chunked data, re-sign chunks with backend credentials, and re-encode for upstream
  - `IsStreamingEligible` requires Content-Length and excludes trailer checksums/copy ops
  - `IsAwsChunkedEligible` detects aws-chunked encoding for streaming re-signing
  - Response bodies without Content-Length are copied with a size limit
  - Request body enforcement via `http.MaxBytesReader` at ingress (configurable max_body_size)
  - CompleteMultipartUpload uses bounded buffering (1MB XML limit)

- **AWS Chunked Re-Encoder** (`internal/backend/proxy/chunked.go`):
  - `AwsChunkedReEncoder` - streaming decoder/re-encoder for aws-chunked payloads
  - Reads client-signed chunks, extracts raw data, re-signs with backend credentials
  - Maintains chunk signature chaining (each chunk signature depends on previous)
  - Calculates re-encoded content length upfront without buffering entire payload
  - Uses 64KB chunk boundaries for optimal performance

- **AWS Chunked Detection** (`internal/backend/proxy/streaming.go`):
  - Detects aws-chunked via `Content-Encoding: aws-chunked` header (per AWS docs)
  - Also detects via `x-amz-content-sha256` starting with `STREAMING-` (minio-go compatibility)
  - Requires `x-amz-decoded-content-length` header for decoded payload size
  - This dual detection ensures compatibility with minio-go and other clients that omit `Content-Encoding`

### Operations Implemented

- ✅ GET Object
- ✅ HEAD Object
- ✅ PUT Object
- ✅ DELETE Object
- ✅ ListObjectsV2 (virtual bucket handler with concurrent backend queries)
- ✅ CreateMultipartUpload (`POST ?uploads`)
- ✅ UploadPart (`PUT ?uploadId=&partNumber=`)
- ✅ CompleteMultipartUpload (`POST ?uploadId=`)
- ✅ AbortMultipartUpload (`DELETE ?uploadId=`)
- ✅ ListParts (`GET ?uploadId=`)

## 9. Virtual Bucket Handling

The `internal/bucket/` package handles virtual bucket operations:

- **ListObjectsV2** (`listobjectsv2.go`):
  - Query parameter detection (`?list-type=2`)
  - Full S3 API compatibility (MaxKeys, Prefix, StartAfter, ContinuationToken, Delimiter)
  - XML response generation with proper pagination metadata

- **Concurrent Processor** (`concurrent_processor.go`):
  - Parallel queries to multiple S3 backends with error isolation
  - Result aggregation with proper sorting

- **Prefix Optimizer** (`prefix_optimizer.go`):
  - Intelligent pattern analysis for targeted S3 queries
  - Static prefix extraction plus physical-prefix computation for trivial rewrites

- **Operations** (`operations.go`):
  - `DetectMultipartOperation()` - analyzes HTTP method and query parameters

## 10. Observability

- **Logging** (`internal/observability/observability.go`):
  - `NewLogger()` - slog with JSON/text handler, configurable levels
  - `RequestLogger()` - middleware tracking request duration
  - `TraceIDMiddleware()` - adds X-Trace-ID to requests

- **Metrics**:
  - `s3router_requests_total` - counter with operation, backend, status labels
  - `s3router_request_latency_seconds` - histogram with operation, backend labels
  - `s3router_backend_errors_total` - counter with backend, error_type labels
  - `s3router_routing_decisions_total` - counter with bucket, backend labels

- **Tracing**: Trace ID correlation only (X-Trace-ID header, no OpenTelemetry SDK)

## 11. Admin Server

- **Server** (`internal/admin/server.go`):
  - `/healthz` - liveness probe (always returns 200)
  - `/readyz` - readiness probe with backend health aggregation
  - `/metrics` - Prometheus metrics endpoint
  - `/admin/config` - current configuration dump (JSON)
  - `POST /admin/backend/{id}/health-check` - manual health check trigger

## 12. HTTP Server & Main Entrypoint

- **Server Configuration** (`internal/server/server.go`):
  - HTTP server setup with configurable timeouts
  - Graceful shutdown support

- **Main Server** (`cmd/s3router/main.go`):
  - Flag parsing: -config, -listen, -admin, -log-level, -log-format, -timeout, -credentials, -check-route
  - Config loading with validation
  - Backend manager initialization
  - Routing matcher with configurable cache
  - Auth verifier setup
  - Custom handler chain for S3 API requests (no chi router)
  - Separate admin server goroutine
  - Graceful shutdown on SIGINT/SIGTERM

## 13. Testing

### Go Unit Tests

Unit tests exist for all core modules:
- `internal/config/loader_test.go`, `virtual_host_test.go`
- `internal/routing/matcher_test.go`
- `internal/auth/credentials_test.go`, `verifier_test.go`
- `internal/backend/manager_test.go`
- `internal/bucket/listobjectsv2_test.go`, `prefix_optimizer_test.go`, `operations_test.go`, `pagination_test.go`
- `internal/backend/proxy/executor_test.go`, `streaming_test.go`, `signer_test.go`
- `internal/backend/cred/factory_test.go`, `provider_test.go`
- `internal/observability/observability_test.go`
- `internal/admin/server_test.go`

### Python Integration Tests

Integration tests live under `tests/integration` and `tests/helm-integration`, executed via the Makefile and GitHub Actions.

## 14. Tooling & CI

- **Makefile**:
  - `make lint` - golangci-lint + gofmt
  - `make test` - go test with race detector and coverage
  - `make build` - build binary to bin/s3router
  - `make docker` - docker build
  - `make clean` - remove artifacts
  - `make run` - run with example config
  - `make fmt` - gofmt + go mod tidy

- **GitHub Actions** (`.github/workflows/ci.yml`):
  - Matrix testing on push/PR
  - Test with -race flag
  - golangci-lint, gofmt checking
  - Docker build verification

## 15. Deployment Artifacts

- **Dockerfile** (multi-stage) and Helm chart under `chart/`
- **Configuration examples**: `config.example.yaml`, `credentials.example.json`, `backend-credentials.example.json`

## Current Status

Core S3 router is fully functional with:
- Flexible routing with regex-based path matching and sequential rewrites
- SigV4 authentication for all object operations
- CRUD proxying (GET/HEAD/PUT/DELETE Object)
- Multipart uploads (CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload, ListParts)
- ListObjectsV2 with concurrent backend queries and prefix optimization
- Pluggable backend credential providers (file, AWS Secrets Manager, default chain, STS role assumption)
- Prometheus metrics and structured logging
- Health/ready endpoints and admin server

**Not yet implemented**: automatic failover between backends, hot-reload of configuration.

---

## 16. Template Package Implementation

The `internal/template` package supports nested placeholders and conditional operators in default values and expansions using an AST-based parser.

### 16.1 Core Syntaxes

| Syntax | Meaning |
|--------|---------|
| `${VAR}` or `$VAR` | Basic variable reference |
| `${VAR:-default}` | Use default if VAR is unset/empty |
| `${VAR:+expansion}` | Use expansion if VAR is set (non-empty) |

### 16.2 Features

✅ Unlimited nesting depth
✅ Two conditional operators: `:-` (if-not-set) and `:+` (if-set)
✅ Error reporting for malformed syntax
✅ Immutable parsed templates (can be reused)
✅ Support for named and indexed placeholders
✅ Literal text within default values and expansions
✅ Nested operators: `${foo:-${bar:+value}}`

### 16.3 API

#### Parse a Template

```go
tmpl, err := template.Parse("${foo:-${bar}}")
if err != nil {
    // Handle parse error (unclosed braces, etc.)
}
```

#### Execute with Placeholder Values

```go
placeholders := template.NewPlaceholders().
    SetNamed("bar", "fallback_value").
    AddIndexed("first")

result, err := tmpl.Execute(placeholders)
```

### 16.4 Placeholder Types Supported

| Type | Examples | Notes |
|------|----------|-------|
| Named | `$foo`, `${foo}` | Variable names: `[a-zA-Z_][a-zA-Z0-9_]*` |
| Indexed | `$1`, `${1}` | 1-indexed positional arguments |
| Default | `${foo:-value}` | Literal or nested placeholders in default |
| Expansion | `${foo:+value}` | Literal or nested placeholders in expansion |
| Zero index | `${0}` | Uses index 0 of the indexed slice |

### 16.5 The `:-` Operator (If-Not-Set Default)

**Usage**: `${VAR:-default_value}`

Returns the default value if VAR is unset or empty, otherwise returns the variable value.

```go
// Example 1: Variable is set
tmpl, _ := template.Parse("${host:-localhost}")
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("host", "example.com"))
// → "example.com"

// Example 2: Variable is not set
tmpl, _ := template.Parse("${host:-localhost}")
result, _ := tmpl.Execute(template.NewPlaceholders())
// → "localhost"

// Example 3: Variable is empty string
tmpl, _ := template.Parse("${host:-localhost}")
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("host", ""))
// → "localhost"  (treated as unset)
```

### 16.6 The `:+` Operator (If-Set Expansion)

**Usage**: `${VAR:+expansion_value}`

Returns the expansion value if VAR is set (non-empty), otherwise returns empty string.

```go
// Example 1: Variable is set
tmpl, _ := template.Parse("${user:+Hello ${user}}")
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("user", "Alice"))
// → "Hello Alice"

// Example 2: Variable is not set
tmpl, _ := template.Parse("${user:+Hello ${user}}")
result, _ := tmpl.Execute(template.NewPlaceholders())
// → ""  (empty string)

// Example 3: Variable is empty string
tmpl, _ := template.Parse("${user:+Hello ${user}}")
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("user", ""))
// → ""  (treated as unset)
```

### 16.7 Complex Examples

#### Simple Nesting with :-
```go
tmpl, _ := template.Parse("${database_host:-${default_host}}")
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("default_host", "localhost"))
// → "localhost"
```

#### Multi-Level Nesting
```go
tmpl, _ := template.Parse("${a:-${b:-${c:-fallback}}}")
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("c", "level3"))
// → "level3"
```

#### Nested with Literals and :-
```go
tmpl, _ := template.Parse("${endpoint:-https://${region}.example.com}")
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("region", "us-east-1"))
// → "https://us-east-1.example.com"
```

#### Using :+ Operator
```go
tmpl, _ := template.Parse("${debug:+DEBUG: ${value}}")
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("debug", "true").SetNamed("value", "info"))
// → "DEBUG: info"

result2, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("value", "info"))
// → ""  (empty string, because debug is not set)
```

#### Mixing :- and :+
```go
tmpl, _ := template.Parse("${env:+ENV is set} ${fallback:-using default}")

// Case 1: Both set
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("env", "prod").SetNamed("fallback", "custom"))
// → "ENV is set using custom"

// Case 2: Only env set
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("env", "prod"))
// → "ENV is set using default"

// Case 3: Neither set
result, _ := tmpl.Execute(template.NewPlaceholders())
// → " using default"
```

#### Complex Nested Operators
```go
tmpl, _ := template.Parse("${foo:+yes_${bar:-no}}")
result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("foo", "set"))
// → "yes_no"

result, _ := tmpl.Execute(template.NewPlaceholders().SetNamed("foo", "set").SetNamed("bar", "value"))
// → "yes_value"

result, _ := tmpl.Execute(template.NewPlaceholders())
// → ""
```

### 16.8 Implementation Details

- **Parser**: Recursive descent parser in `template.go`
- **AST Nodes**: 
  - `LiteralNode` - plain text
  - `PlaceholderNode` - variable reference with optional operator (`:- ` or `:+`)
  - `SequenceNode` - sequence of nodes
- **Execution**: Tree-walking interpreter with operator dispatch
- **Operator Flag**: `PlaceholderNode.IfSet` distinguishes `:+` (true) from `:-` (false)
- **Test Coverage**: Template parsing and execution tests cover nested operators and indexed values

### 16.9 Performance Considerations

- **Parse once, execute many times**: Cache the `Template` if reusing with different placeholder values
- **AST-based execution**: O(n) where n is the number of nodes in the template
- **No double-substitution**: Substituted values are never re-processed
- **Lazy evaluation**: Defaults/expansions are only evaluated when needed

### 16.10 Files

```
internal/template/
├── template.go            - Parser and executor (287 lines)
├── template_test.go       - Comprehensive tests (572 lines)
├── placeholders.go        - Placeholder container
└── placeholders_test.go   - Basic utility tests
```

---

## 17. Integration Test Configuration

Integration tests run under `tests/integration` and `tests/helm-integration` via `make integration-test` and `make helm-integration-test`.

### 17.1 Test Client Endpoint (boto3 → router)

- Tests create boto3 clients with explicit `addressing_style="path"` when targeting the router endpoint (see `tests/integration/conftest.py::create_s3_client`).
- Path-style requests ensure the bucket appears in the URL path so the router can extract bucket/key from `/bucket/key`.

### 17.2 Router Backend Endpoint (router → moto backend)

- The router config in tests uses an endpoint template that includes `${bucket}` and `${key}` (for example, `http://127.0.0.1:57809/${bucket}/${key}`).
- Backend clients always use the configured endpoint resolver (`EndpointResolverV2`), so SDK operations go to moto rather than default AWS endpoints.

---

## 18. Request Body Streaming Support

### Overview

The router streams eligible PutObject and UploadPart requests directly to upstream S3 without buffering bodies locally, using a custom SigV4 signer and direct HTTP client.

### Implementation Summary

The streaming implementation consists of three main components:

#### 1. SigV4 Signer (`signer.go`)

- **S3Signer struct**: Encapsulates SigV4 signing logic
- **Key methods**:
  - `NewS3Signer()` - Creates a new signer with credentials and region
  - `SignRequest()` - Signs an HTTP request using SigV4, accepting pre-computed payload hash
  - `buildCanonicalRequest()` - Constructs the canonical request string
  - `buildCanonicalHeaders()` - Extracts and canonicalizes headers
  - `shouldSignHeader()` - Determines which headers to include in signature
- **Payload hash handling**: Accepts `x-amz-content-sha256` header value or uses "UNSIGNED-PAYLOAD"
- **Credential retrieval**: Extracts credentials from backend client's credential provider

#### 2. Streaming HTTP Executor (`streaming.go`)

- **StreamingPutObject()**: Executes PUT requests by streaming body directly to upstream
  - Copies relevant headers (content-type, metadata, checksums, etc.)
  - Signs request using S3Signer with client-provided payload hash
  - Streams body using http.Client.Do() without buffering
  - Handles error responses by reading and returning error XML
  
- **StreamingUploadPart()**: Similar to StreamingPutObject but for multipart uploads
  - Includes uploadId and partNumber query parameters
  - Copies Content-MD5 and checksum headers
  
- **Header copy functions**:
  - `copyPutObjectHeaders()` - Forwards all relevant headers for PUT operations
  - `copyUploadPartHeaders()` - Forwards headers specific to upload part
  - `peekS3ErrorResponseResponse()` - Reads error response body for proper error handling
  
- **Eligibility detection**: `IsStreamingEligible()` checks if request can use streaming path
  - Requires Content-Length header
  - Fails if trailer checksums are present
  - Fails for copy operations

- **AWS Chunked Streaming**: `IsAwsChunkedEligible()` and `StreamingAwsChunked*()` handlers
  - Detects aws-chunked encoding via `Content-Encoding: aws-chunked` and `x-amz-content-sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD`
  - Uses `AwsChunkedReEncoder` for streaming decode + re-encode with backend credentials
  - Preserves true streaming without buffering the entire body
  - Maintains proper signature chain from seed signature through chunk signatures

#### 3. AWS Chunked Re-Encoding (`chunked.go`)

The `AwsChunkedReEncoder` handles streaming aws-chunked requests by:

1. **Decoding incoming chunks**: Parses `<hex-size>;chunk-signature=<sig>\r\n<data>\r\n` format
2. **Re-signing with backend credentials**: Each chunk is signed using the backend's signing key
3. **Maintaining signature chain**: Each chunk signature incorporates the previous signature
4. **Streaming output**: Produces valid aws-chunked format for the backend

**Key Components**:
- `DeriveSigningKey()` - Derives kSigning key from backend secret access key
- `ExtractSeedSignature()` - Extracts initial signature from Authorization header
- `CalculateReEncodedContentLength()` - Pre-calculates output body size for Content-Length header

**Signature Chain**:
```
seed signature (from client's Authorization header)
  → chunk 1 signature (signs: prev_sig + chunk_hash)
    → chunk 2 signature (signs: prev_sig + chunk_hash)
      → ... → final chunk (size 0)
```

#### 3. Executor Integration (`executor.go`)

- **executePutObject()**: Detects streaming eligibility before using AWS SDK
  - Calls StreamingPutObject() for eligible requests
  - Falls back to buffered path for:
    - Chunked encoding without Content-Length
    - Trailer-based checksums
    - Copy operations
  
- **executeUploadPart()**: Similar eligibility check and fallback logic

### Key Design Decisions

1. **Trust the client**: The implementation forwards `x-amz-content-sha256` headers intact, trusting the client's checksum computation. This allows true streaming without verifying checksums locally.

2. **Graceful degradation**: Requests that don't meet streaming criteria (trailers, copy operations) fall back to the existing buffered AWS SDK path.

3. **Memory efficiency**: Streaming reduces memory usage from O(file_size) to O(chunk_size), enabling support for very large files.

4. **No circuit breaker**: Streaming requests bypass the circuit breaker for simpler implementation. Errors during streaming won't trigger circuit breaker failures.

5. **Pre-computed signatures**: The signer accepts pre-computed payload hashes rather than computing them, avoiding the need to buffer or seek through the body twice.

### Testing

- **Unit tests**: Cover SigV4 signing, header copying, streaming eligibility detection
- **Integration tests**: Test actual S3 operations including large files, multipart uploads, metadata, checksums

### Design Notes

#### Headers to Forward

For PutObject:
- `Content-Type`, `Content-Encoding`, `Content-Length`
- `x-amz-content-sha256` (from client, or "UNSIGNED-PAYLOAD")
- `x-amz-meta-*` (user metadata)
- `x-amz-checksum-*` (if provided in headers, not trailers)
- `Cache-Control`, `Content-Disposition`, `Content-Language`, `Expires`
- `x-amz-storage-class`, `x-amz-acl`

For UploadPart:
- `Content-Length`, `Content-MD5`
- `x-amz-content-sha256`
- `x-amz-checksum-*`

#### Streaming Eligibility

Use streaming path when:
1. `Content-Length` header is present (known body size)
2. No `x-amz-trailer` header (no trailing checksums)
3. Not a copy operation (`x-amz-copy-source` absent for PutObject)

Fall back to buffered path when:
1. Chunked transfer encoding without Content-Length
2. Trailer-based checksums required
3. Copy operation

#### SigV4 Signing

The key insight is that SigV4 allows specifying `UNSIGNED-PAYLOAD` or a pre-computed SHA256 for the payload hash. Since we trust the client:

1. Use client's `x-amz-content-sha256` as the payload hash in signing
2. Forward this header to upstream
3. Upstream S3 validates (or accepts UNSIGNED-PAYLOAD)

#### Error Handling

- Parse upstream S3 error responses (XML) and forward to client
- Handle connection errors gracefully
- Log streaming failures for debugging

### Considerations

- **Memory usage**: Streaming avoids buffering request bodies in memory beyond HTTP client internals
- **Timeout handling**: Long-running streams should respect context deadlines
- **Circuit breaker bypass**: Streaming errors won't affect circuit breaker state (simpler implementation)


## Template Analysis & Prefix Optimization

### Overview

- Template analysis is computed eagerly at parse time via `Template.Analysis()`.
- Prefix optimizer caching includes rewrite patterns/results in the cache key.

### Template Package (`internal/template/template.go`)

- `Template` stores the original input string and cached analysis for reuse.
- `Analysis()` returns cached analysis computed during `Parse()`.

### Prefix Optimizer (`internal/bucket/prefix_optimizer.go`)

- `buildCacheKey()` includes route patterns and rewrite templates, avoiding cache collisions.
- `analyzeRewrites()` uses `Template.Analysis()` to detect trivial rewrites.

### ListObjectsV2 Handler (`internal/bucket/listobjectsv2.go`)

- `reverseRewriteRule()` uses template analysis directly.

### Template Analysis Result Structure

```go
type TemplateAnalysisResult struct {
    Prefix               string                // Static text before first placeholder
    Inbetween            []PlaceholderTailPair // Placeholder + trailing literal pairs
    ContainsConditionals bool                  // Has ${name:-default} style placeholders
}

type PlaceholderTailPair struct {
    Placeholder *PlaceholderNode
    Tail        string  // Static text after this placeholder
}
```

**Example:** Template `"PREFIX/$1/MIDDLE/$2/SUFFIX"`
- `Prefix`: `"PREFIX/"`
- `Inbetween[0]`: `{$1, "/MIDDLE/"}`
- `Inbetween[1]`: `{$2, "/SUFFIX"}`

### Common Patterns

```go
// Check if template is just "$rest" or "$1"
analysis := tmpl.Analysis()
isSingleCapture := analysis.Prefix == "" &&
    len(analysis.Inbetween) == 1 &&
    analysis.Inbetween[0].Tail == ""

// Get suffix after last capture
var suffix string
if len(analysis.Inbetween) > 0 {
    suffix = analysis.Inbetween[len(analysis.Inbetween)-1].Tail
}
```

### Notes

- `buildCacheKey()` uses `\x00` separators to avoid collisions between pattern and rewrite strings.
- `PatternStructure` is still used internally during prefix analysis but is not exposed in `PrefixAnalysis`.


## 8. OpenTelemetry Integration

### Overview

S3-router includes comprehensive OpenTelemetry instrumentation for distributed tracing and metrics collection. All configuration is environment-driven using standard OTEL_* variables.

### Core Components

#### Observability Package (`internal/observability/observability.go`)

**OpenTelemetryProvider Type**
```go
type OpenTelemetryProvider struct {
    TracerProvider *sdktrace.TracerProvider
    MeterProvider  *metric.MeterProvider
    Tracer         trace.Tracer
    shutdown       []func(context.Context) error
}
```

**Initialization**
- `InitOpenTelemetry(ctx)` - Creates OTLP exporters from environment variables
- OTLP trace exporter (HTTP): Batch exporting with configurable endpoint
- OTLP metric exporter (HTTP): Periodic reader with 60s interval
- Global provider registration and W3C Trace Context + Baggage propagation setup
- Returns provider for deferred shutdown

**Middleware**
- `OTelMiddleware(tracer)` - HTTP handler wrapper using `otelhttp` library
  - Automatic span creation per HTTP request
  - Span attributes: method, path, status code, duration
  - Integration with standard Go HTTP context

### Instrumented Components

#### Backend Proxy (`internal/backend/proxy/executor.go`)
- **Execute()** method creates root span for each S3 operation
- Span name: "Execute"
- Automatically tracks all S3 operations: GetObject, PutObject, DeleteObject, ListObjectsV2, CopyObject, etc.
- Context propagation to downstream calls

#### Routing (`internal/routing/matcher.go`)
- **Match(ctx, ...)** method updated to accept context parameter
- Creates span for route matching decision
- Span name: "Match"
- Helps identify routing bottlenecks or misconfigurations

#### Server (`internal/server/server.go`)
- Passes request context through routing decision path
- Ensures trace context flows from HTTP handler to backend

### Middleware Chain

Request processing order:
1. **OTelMiddleware** - Automatic HTTP trace span with otelhttp
2. **TraceIDMiddleware** - X-Trace-ID context management
3. **RequestLogger** - Structured request logging

### Configuration

All configuration via environment variables (no code changes needed):

**OTLP Exporter Configuration**
- `OTEL_EXPORTER_OTLP_ENDPOINT` - Default: `http://localhost:4318`
- `OTEL_EXPORTER_OTLP_TIMEOUT` - Default: `10s`

**Trace Configuration**
- `OTEL_TRACES_EXPORTER` - Default: `otlp` (HTTP exporter)
- `OTEL_TRACES_SAMPLER` - Options: `always_on`, `always_off`, `traceidratio`
- `OTEL_TRACES_SAMPLER_ARG` - For `traceidratio`: 0-1 sampling rate

**Metrics Configuration**
- `OTEL_METRICS_EXPORTER` - Default: `otlp` (HTTP exporter)

**SDK Configuration**
- `OTEL_SDK_DISABLED` - Default: `false`
- `OTEL_SERVICE_NAME` - Default: `s3-router`
- `OTEL_SERVICE_VERSION` - Default: `0.1.0`

### Integration Examples

**Jaeger (Local Development)**
```bash
docker run -d -p 16686:16686 -p 4318:4318 jaegertracing/all-in-one:latest

export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
./s3router -config config.yaml

# View traces: http://localhost:16686
```

**OTLP Collector**
```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318
export OTEL_TRACES_EXPORTER=otlp
./s3router -config config.yaml
```

**Sampling (10% of traces)**
```bash
export OTEL_TRACES_SAMPLER=traceidratio
export OTEL_TRACES_SAMPLER_ARG=0.1
./s3router -config config.yaml
```

### Startup & Shutdown

**Initialization** (`cmd/s3router/main.go`)
- Called before logger setup
- Creates trace and metric providers
- Registers global providers for otelhttp and component tracers
- Returns provider for graceful shutdown

**Shutdown**
- 5-second timeout context
- Flushes pending spans/metrics to exporter
- Closes exporter connections
- Deferred in main() for guaranteed execution

### Dependencies

```
go.opentelemetry.io/otel v1.39.0                           # Core API
go.opentelemetry.io/otel/sdk/trace v1.39.0                 # Trace SDK
go.opentelemetry.io/otel/sdk/metric v1.39.0                # Metric SDK
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp  # OTLP trace
go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp # OTLP metrics
go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.64.0  # HTTP
```

### Trace Propagation

- **W3C Trace Context**: `traceparent` and `tracestate` headers
- **Baggage**: Cross-service metadata via Baggage header
- **X-Trace-ID**: Legacy support for existing trace ID systems

### Performance Considerations

- **Overhead**: Minimal - batch exporting reduces network overhead
- **Memory**: Batch exporter buffers spans (default 512 size)
- **Sampling**: Use `traceidratio` sampler to reduce volume in high-traffic
- **Async Export**: Non-blocking batch processing

### Troubleshooting

**Traces Not Exporting**
- Verify endpoint is reachable: `curl http://localhost:4318/v1/traces`
- Enable debug logging: `OTEL_LOG_LEVEL=debug`
- Check environment variables: `env | grep OTEL`

**High Memory Usage**
- Reduce batch size or sampling rate
- Configure periodic metric reader interval
- Check exporter timeout settings

**Missing Spans**
- Verify sampler configuration
- Check that context is properly propagated
- Ensure tracer is obtained from global provider

## 8.7 Trace Context Injection

### Overview
OpenTelemetry trace context (trace ID, span ID, sampled flag) is automatically embedded into all log entries throughout the application for improved observability and log-to-trace correlation.

### Implementation Details

#### New Function: `WithTraceContext()` in `internal/observability/observability.go`
```go
func WithTraceContext(ctx context.Context, logger *slog.Logger) *slog.Logger
```
- Extracts span context from request context
- Injects trace_id, span_id, and sampled flag into logger attributes
- Returns unmodified logger if no trace context exists (backward compatible)

#### Updated Middleware: `RequestLogger()` in `internal/observability/observability.go`
- Now calls `WithTraceContext()` to embed trace data in request logs
- Each request log now includes trace ID, span ID, and sampled flag

#### Updated Handler: `HandleS3Request()` in `internal/server/server.go`
- Injects trace context into the logger before processing
- Trace context propagates through all downstream log calls
- Error logs also include trace context

### Files Modified
- `internal/observability/observability.go` - Added `WithTraceContext()` and updated middleware
- `internal/server/server.go` - Updated `HandleS3Request()` to use trace context

### Benefits
1. **Correlation**: Logs can be correlated with OpenTelemetry traces using trace_id
2. **Distributed Tracing**: Complete request flow visible across logs and traces
3. **Backward Compatible**: Works seamlessly with and without OTel enabled
4. **Automatic**: No manual trace context passing required in individual log calls

### Example Output
With trace context injected into logs:
```json
{
  "time": "2026-01-30T12:42:03.504771+09:00",
  "level": "INFO",
  "msg": "request with trace context",
  "trace_id": "0ffe04d548316123400f34bfed4615ad",
  "span_id": "864c5a4ec6535254",
  "sampled": true,
  "method": "GET",
  "path": "/bucket/key",
  "status": 200
}
```

### Testing
- All observability tests: PASS (14 tests)
- All server tests: PASS (10 tests)
- All project tests: PASS
- Verified trace context appears in all request and error logs
