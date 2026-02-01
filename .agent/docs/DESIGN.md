# S3 Router – Design Overview

## 1. Goals

Provide an HTTP server that presents an Amazon S3-compatible API surface while routing each request to one of many upstream S3-compatible backends. The system preserves S3 semantics for the implemented operations (SigV4 authentication, basic CRUD, multipart uploads, ListObjectsV2) and provides declarative routing, credential management, and observability.

## 2. Architecture

1. **Front-End HTTP Layer**: Terminates TLS/HTTP, parses S3-style routes, enforces SigV4 auth for object/listing requests, and emits structured request context objects.
2. **Routing & Rewrite Engine**: Matches bucket/object paths against ordered regex routes, applies rewrite rules (with named/numbered captures), and selects the target backend. Optional LRU caching is supported.
3. **Backend Manager & Proxy**: Creates AWS SDK v2 S3 clients per backend with optional endpoint override and pluggable credential providers. Executes proxied S3 operations and handles streaming. Provides health checks and a circuit breaker per backend.
4. **Virtual Bucket Handler**: Handles ListObjectsV2 with concurrent multi-backend queries and intelligent prefix optimization.
5. **Observability & Admin Surfaces**: Structured logging (slog), distributed tracing via OpenTelemetry (OTLP HTTP), Prometheus metrics with OTLP export, W3C Trace Context propagation, and an admin server with health/ready/config endpoints.
6. **Configuration**: YAML/JSON/TOML config loaded via Viper, with validation and regex compilation at load time.

## 3. Data & Configuration Model

- **Backends**: Named records containing bucket name, optional endpoint override, credential configuration, prefix, timeout, and retry settings.
- **Buckets**: Logical buckets the router exposes; each references an ordered list of route rules.
- **Bucket Lifecycle**: Bucket creation/deletion is declarative; operators add or remove entries from the config. Runtime CreateBucket/DeleteBucket calls never mutate state—they return policy errors indicating configuration-managed lifecycle. There is no hot-reload; changes require restart.
- **Routes**: Regex path matchers referencing a backend and optional rewrite stack. Routing is based on path regex only.
- **Rewrites**: Sequence of pattern/result pairs supporting named captures and substitution tokens to transform object keys prior to proxying.
- **Credentials**:
  - **Client authentication**: Static keys for SigV4 verification are loaded from a JSON credentials store.
  - **Backend credentials**: Pluggable credential providers including:
    - `default`: AWS SDK default credential chain (environment, shared config, IMDS)
    - `file`: JSON file on filesystem
    - `aws-secrets-manager`: AWS Secrets Manager secret
    - `inline`: Static credentials in config (not recommended)
    - All providers support optional STS role assumption for cross-account access.
- **Feature Flags**: Optional boolean map parsed from config; no built-in behaviors are gated today.

## 4. Request Lifecycle

1. Receive HTTP request and parse S3-style bucket/object.
2. Authenticate SigV4 (header or query) against configured credentials.
3. Resolve bucket configuration, iterate routes to find first match, apply rewrite pipeline.
4. Acquire backend client via manager and execute the AWS SDK request (GET/HEAD/PUT/DELETE, multipart operations).
5. Translate upstream response into S3-compatible response codes/headers; emit logs/metrics.

### Bucket Management Semantics

1. **CreateBucket/DeleteBucket**: Intercepted at the HTTP layer and short-circuited with a policy error (`409 BucketAlreadyExists` for create, `403 BucketNotEmpty` for delete). These calls never mutate state.
2. **ListBuckets**: Synthesized response enumerating the logical buckets defined in configuration with a synthetic creation timestamp.

## 5. Authentication Strategy

### Credential Storage & Verification
- **Backend**: Static credential storage (file-based JSON)
  - Maps access key IDs to secret keys for SigV4 verification
  - Configuration file with restricted permissions (0600)
  
### SigV4 Signature Verification
- **Validation**: Full signature verification (not just metadata extraction)
  - Compute canonical request digest from incoming request
  - Derive signing key using stored secret and timestamp
  - Compare computed vs. provided signature (header and query-string variants)
  - Timestamp validation (±15 minutes) to prevent replay attacks
  
### Authentication Enforcement
- **Policy**: Strict enforcement on all object operations and ListObjectsV2 (bucket-level control-plane calls are intercepted without auth).
  - All requests must include valid SigV4 signature
  - Invalid/missing auth returns 401 Unauthorized
  - Malformed signatures return 400 Bad Request

## 6. Observability

### Distributed Tracing (OpenTelemetry)
- **OTLP HTTP Exporter**: Traces and metrics exported to configurable OTLP endpoint (env: `OTEL_EXPORTER_OTLP_ENDPOINT`)
- **Batch Exporting**: Efficient batching with automatic shutdown handling
- **Trace Propagation**: W3C Trace Context and Baggage headers for cross-service tracing
- **Instrumentation**: Automatic HTTP spans via middleware, custom spans for routing and backend operations
- **Configuration**: Environment-driven via OTEL_* variables (no code config needed)

### Metrics & Logging
- **Structured Logging**: JSON/text output via Go stdlib slog
- **Prometheus Metrics**: RequestTotal, RequestLatency, BackendErrors, RoutingDecisions
- **Request Logging**: Duration, method, path, status code
- **Trace ID Context**: X-Trace-ID header support for backward compatibility

## 7. AWS Chunked Encoding Compatibility

### Background

AWS S3 supports streaming uploads with chunked payload signing via the `STREAMING-AWS4-HMAC-SHA256-PAYLOAD` algorithm. According to AWS documentation, clients should send:
- `Content-Encoding: aws-chunked`
- `x-amz-content-sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD`
- `x-amz-decoded-content-length: <original size>`
- `Content-Length: <encoded size>`

### Client Compatibility

Some S3 client libraries (notably minio-go) implement streaming uploads without setting the `Content-Encoding: aws-chunked` header. They only set:
- `x-amz-content-sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD`
- `x-amz-decoded-content-length: <original size>`

This appears to work against AWS S3, which detects streaming mode from the `x-amz-content-sha256` header value.

### Detection Strategy

The s3-router detects AWS chunked encoding via **either**:
1. `Content-Encoding` header containing `aws-chunked` (per AWS documentation), **OR**
2. `x-amz-content-sha256` header starting with `STREAMING-` (e.g., `STREAMING-AWS4-HMAC-SHA256-PAYLOAD`, `STREAMING-UNSIGNED-PAYLOAD-TRAILER`) combined with `x-amz-decoded-content-length` header

This dual detection ensures compatibility with both strictly compliant clients and clients like minio-go that omit the `Content-Encoding` header.

## 8. Key Cross-Cutting Concerns

- **Security**: Strict SigV4 verification (header and query), constant-time signature comparison, clock-skew protection.
- **Performance**: Connection pooling, routing decision caching, concurrent ListObjectsV2 backend queries with prefix optimization.
- **Resilience**: Circuit breakers per backend with optional SDK retries configured per backend. No fallback routing is implemented.
- **Observability**: Full end-to-end tracing with OpenTelemetry, batch span exporting, configurable exporters.
- **Testing Strategy**: Unit tests for config, routing, auth, backend, and observability; integration tests under tests/integration.

## 9. Deployment Considerations

- Docker/OCI image and Helm chart live in this repository.
- Stateless workers; no shared cache or hot-reload.
- Health/ready/metrics/config endpoints exposed via admin server.
- OpenTelemetry tracing configured via environment variables at runtime.
