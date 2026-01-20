# s3-router

s3-router is a S3 proxy server that intelligently routes requests to multiple S3-compatible backends based on flexible regex patterns and key transformations.

## Features

* **S3 API Compatibility** - S3 API compatibility for object operations (GET, PUT, DELETE, etc.), multipart uploads, and bucket operations
* **Flexible Request Routing** - Route requests based on ordered regex patterns on object keys with deterministic rewrites
* **Request Rewriting** - Multi-step rewrite rules with named and numbered capture group templates for transforming object keys
* **Multiple Backend Support** - Proxy requests to multiple S3-compatible backends with independent configuration and credentials
* **Virtual Host Support** - S3 virtual-hosted-style requests with explicit host mappings and wildcard/suffix matching
* **Advanced Credential Management** - Support for multiple credential sources:
  - AWS SDK default credential chain (IAM roles, environment variables, shared credentials)
  - File-based credentials with JSON format
  - AWS Secrets Manager integration for centralized secret management
  - STS role assumption for cross-account access
  - Inline static credentials (not recommended for production)
* **SigV4 Authentication** - Full AWS SigV4 signature verification with support for header-based and query-based presigned URLs
* **Circuit Breaker Pattern** - Automatic failure detection and backend isolation with automatic recovery
* **Request/Response Streaming** - Optimized streaming for large file uploads and downloads with minimal memory usage
* **Route Caching** - LRU cache for route matching decisions to improve performance
* **Observability** - Prometheus metrics for monitoring request latency, throughput, and backend health
* **Health Checks** - Built-in health check endpoints and per-backend health monitoring
* **Configurable Timeouts** - Per-backend and server-level timeout configuration
* **Multi-format Configuration** - Support for YAML, JSON, and TOML configuration files
* **Comprehensive Logging** - Human-friendly and structured JSON logging with automatic format detection

### Configuration language

s3-router loads its routing table from a JSON, YAML, or TOML document with the following structure.

```yaml
backends:        # map keyed by backend id
  <backend-name>:
    endpoint: s3.ap-northeast-1.amazonaws.com  # optional override; defaults to AWS endpoint logic
    bucket: bucket-1                           # required physical bucket name
    prefix: prefix1/                           # optional prefix prepended to every rewritten key
    timeout: 30s                               # optional per-backend HTTP client timeout (Go duration string)
    retries: 3                                 # optional retry count for AWS SDK requests
    credentials:                               # optional; falls back to AWS default chain when omitted
      type: file                               # credential source type: default, file, aws-secrets-manager, inline
      path: /etc/s3router/backend-creds.json  # required for type: file
      assume_role:                             # optional STS role assumption
        role_arn: arn:aws:iam::1234:role/example
        session_name: s3-router                # optional; defaults to "s3-router"
        duration: 3600                         # optional; duration in seconds (default: 3600)

buckets:         # ordered list evaluated per virtual bucket
  - name: foo                               # virtual bucket exposed to clients
    routes:                                 # first route that matches wins
      - path: ^foo/(?P<rest>.*)             # required RE2 regex applied to the object key
        backend: backend1
        rewrite:                            # optional sequential rewrite steps
          - result: $rest                   # template applies captures from `path`
          - pattern: ^special/(.*)          # optional secondary regex on current key
            result: SPECIAL/$1              # supports numbered/named captures ($1, $rest)

# Server configuration (optional; defaults shown)
server:
  read_timeout: 15
  write_timeout: 15
  idle_timeout: 60
  max_body_size: 4294967296
  route_cache_size: 1000

# Authentication configuration (optional; defaults shown)
auth:
  default_region: us-east-1
  clock_skew_leeway: 15m

# Virtual hosts configuration (optional; defaults shown)
virtual_hosts:
  hosts: []

features:                                  # optional boolean map (currently unused)
  enable_caching: true
  enable_metrics: false

credentials_store: credentials.json         # optional; path to JSON file with static credentials
```

#### Backends

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `endpoint` | string | no | Custom S3-compatible endpoint with optional template placeholders. Defaults to AWS regional/global endpoint resolution. See [Endpoint Configuration](#endpoint-configuration). |
| `bucket` | string | yes | Physical bucket that stores the objects. |
| `prefix` | string | no | Static prefix prepended before the rewritten key (e.g., `folder/`). |
| `timeout` | string | no | Go duration parsed timeout applied to the backend HTTP client (e.g., `30s`, `2m`). |
| `retries` | int | no | Retry attempts for SDK operations. Defaults to the SDK's standard behavior. |
| `credentials` | object | no | Credential source specification with optional role assumption. See [Credential Sources](#credential-sources). |

#### Endpoint Configuration

The `endpoint` field in backend configuration supports two modes:

**Mode 1: Template-based Endpoint (Custom)**

When the `endpoint` field contains placeholders, s3-router resolves them to construct a custom endpoint URL. This is useful for S3-compatible services (MinIO, Wasabi, DigitalOcean Spaces, etc.) or AWS regions with special requirements.

**Supported Placeholders:**

| Placeholder | Format | Description | Example |
| --- | --- | --- | --- |
| `bucket` | `${bucket}` or `$bucket` | The physical bucket name | `my-bucket` |
| `region` | `${region}` or `$region` | AWS region from backend or client context | `us-west-2` |
| `key` | `${key}` or `$key` | The object key (URL-encoded, preserving `/`) | `folder/myfile.txt` |
| `fips` | `${fips}` or `$fips` | Set to `1` if FIPS is enabled, empty otherwise | `1` or `` |
| `globalEndpoint` | `${globalEndpoint}` or `$globalEndpoint` | Set to `1` if using global endpoint, empty otherwise | `1` or `` |
| `accelerate` | `${accelerate}` or `$accelerate` | Set to `1` if S3 Transfer Acceleration is enabled, empty otherwise | `1` or `` |
| `dualStack` | `${dualStack}` or `$dualStack` | Set to `1` if dual-stack (IPv4/IPv6) is enabled, empty otherwise | `1` or `` |
| `usePathStyle` | `${usePathStyle}` or `$usePathStyle` | Set to `1` if using path-style URLs, empty otherwise | `1` or `` |

**Default Values:**

Placeholders support optional default values using the `${name:-default}` syntax:

```yaml
endpoint: "https://${region:-us-east-1}.example.com/${bucket}/${key}"
```

If `${region}` is empty, `us-east-1` will be used.

**Nested and Conditional Placeholders:**

The template system supports unlimited nesting depth and conditional expansion operators:

1. **Nested defaults**: `${VAR:-${FALLBACK:-default}}`
   - Falls back through multiple levels until a value is found
   
   ```yaml
   endpoint: "https://${custom_host:-${region}.example.com}/${bucket}/${key}"
   ```
   If `custom_host` is unset, uses `${region}.example.com`; if both are unset, uses just the bucket and key path.

2. **Conditional expansion (`:+` operator)**: `${VAR:+expansion}`
   - If VAR is set (non-empty), substitutes the expansion; otherwise empty string
   
   ```yaml
   endpoint: "https://s3${fips:+-fips}.${region}.amazonaws.com/${bucket}/${key}"
   ```
   If `fips` is set to `1`, the endpoint becomes `https://s3-fips.{region}.amazonaws.com/...`; otherwise `https://s3.{region}.amazonaws.com/...`

3. **Complex nesting**: Both operators can be combined with arbitrary nesting depth
   ```yaml
   endpoint: "https://${host:+${host}}${host:-minio.${region:-us-east-1}.local}/${bucket}/${key}"
   ```

**Scheme Handling:**

If the resolved endpoint URL doesn't start with `http://` or `https://`, the router automatically prepends `https://`.

**Template Examples:**

```yaml
# AWS S3 virtual-host-style (path-style not recommended for most cases)
endpoint: "https://${bucket}.s3.${region}.amazonaws.com/${key}"

# AWS S3 path-style
endpoint: "https://s3.${region}.amazonaws.com/${bucket}/${key}"

# MinIO on custom domain
endpoint: "https://minio.example.com/${bucket}/${key}"

# S3-compatible with conditional FIPS
endpoint: "https://s3${fips:--fips}.${region}.amazonaws.com/${bucket}/${key}"

# With default region
endpoint: "https://s3.${region:-us-east-1}.example.com/${bucket}/${key}"
```

**Mode 2: AWS SDK Default Endpoint (When not using template)**

When `endpoint` is omitted from the backend configuration, s3-router uses the AWS SDK's endpoint resolver. This automatically selects the correct regional or global endpoint based on:
- The `region` field in backend config (if present)
- Endpoint options: `use_fips`, `use_global_endpoint`, `use_dual_stack`, `accelerate`

The SDK endpoint resolver supports:
- **Regional endpoints**: `https://s3.{region}.amazonaws.com` or virtual-host style
- **FIPS endpoints**: `https://s3-fips.{region}.amazonaws.com`
- **Dual-stack**: IPv4 and IPv6 support
- **Transfer Acceleration**: `https://s3-accelerate.amazonaws.com` or region-specific accelerate endpoints

```yaml
# Uses SDK resolver with regional endpoint
backends:
  backend1:
    bucket: my-bucket
    region: eu-west-1
    # Resolved to: https://s3.eu-west-1.amazonaws.com or virtual-host style

  # Uses SDK resolver with FIPS endpoint
  backend2:
    bucket: my-bucket
    region: us-east-1
    use_fips: true
    # Resolved to: https://s3-fips.us-east-1.amazonaws.com

  # Uses SDK resolver with multiple options
  backend3:
    bucket: my-bucket
    region: us-west-2
    use_fips: true
    use_dual_stack: true
    accelerate: true
```

#### Credential Sources

Backend credentials are specified using the `credentials` field, which supports multiple source types. When credentials are omitted entirely, the AWS SDK default credential chain is used (environment variables, shared credentials file, IMDS, etc.).

##### Credential Source Types

**`type: default` - AWS SDK Default Credential Chain**

Uses the AWS SDK default credential chain, which includes:
- Environment variables (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`)
- Shared credentials file (`~/.aws/credentials`)
- Shared config file (`~/.aws/config`)
- IMDSv2 (EC2 instance metadata)
- ECS task credentials
- WebIdentity token (for EKS, OIDC)

```yaml
credentials:
  type: default
```

**`type: inline` - Inline Static Credentials**

> ⚠️ **Not Recommended**: Embedding credentials in configuration files is a security risk. Use `default`, `file`, or `aws-secrets-manager` instead.

Embeds credentials directly in the configuration file.

```yaml
credentials:
  type: inline
  access_key_id: AKIA...
  secret_access_key: ...
  session_token: optional_temporary_token  # optional
```

**`type: file` - Load from JSON File**

Loads credentials from a JSON file on the filesystem. Useful for containerized deployments with mounted secrets or Kubernetes mounted ConfigMaps/Secrets.

```yaml
credentials:
  type: file
  path: /etc/s3router/backend-creds.json
```

The JSON file format:
```json
{
  "access_key_id": "AKIA...",
  "secret_access_key": "...",
  "session_token": "optional_temporary_token"
}
```

**`type: aws-secrets-manager` - Load from AWS Secrets Manager**

Loads credentials from an AWS Secrets Manager secret. The secret must contain a JSON object with the same format as the file provider.

```yaml
credentials:
  type: aws-secrets-manager
  secret_name: s3-router/my-backend
  region: us-east-1  # optional; defaults to us-east-1
```

The secret value in AWS Secrets Manager:
```json
{
  "access_key_id": "AKIA...",
  "secret_access_key": "...",
  "session_token": "optional_temporary_token"
}
```

##### STS Role Assumption

All credential sources support optional STS role assumption, enabling cross-account or cross-role access patterns:

```yaml
credentials:
  type: file
  path: /etc/s3router/base-creds.json
  assume_role:
    role_arn: arn:aws:iam::123456789:role/S3RouterRole
    session_name: s3-router-backend1  # optional; defaults to "s3-router"
    duration: 3600  # optional; duration in seconds (default: 3600)
```

The flow:
1. Load base credentials from the specified source
2. Use the base credentials to call STS `AssumeRole`
3. Return the temporary credentials from the STS response
4. Use temporary credentials for S3 operations

##### Examples

**Example 1: Default AWS SDK Chain**
```yaml
backends:
  backend1:
    bucket: my-bucket
    credentials:
      type: default
```

**Example 2: File-based Credentials**
```yaml
backends:
  backend2:
    bucket: my-bucket
    credentials:
      type: file
      path: /run/secrets/s3-backend-creds.json
```

**Example 3: AWS Secrets Manager**
```yaml
backends:
  backend3:
    bucket: my-bucket
    credentials:
      type: aws-secrets-manager
      secret_name: prod/s3-router/backend3
      region: us-west-2
```

**Example 4: File-based with Role Assumption**
```yaml
backends:
  backend4:
    bucket: my-bucket
    credentials:
      type: file
      path: /etc/s3router/cross-account-base-creds.json
      assume_role:
        role_arn: arn:aws:iam::987654321:role/CrossAccountS3Access
        session_name: s3-router-cross-account
        duration: 7200
```

**Example 5: Inline Credentials (Not Recommended)**
```yaml
backends:
  backend5:
    bucket: my-bucket
    credentials:
      type: inline
      access_key_id: AKIA...
      secret_access_key: ...
```

> ⚠️ **Not Recommended**: Embedding credentials in configuration files is a security risk. Use `type: default`, `type: file`, or `type: aws-secrets-manager` instead.

#### Buckets and routes

* Buckets are evaluated in the order listed. `name` defines the virtual bucket clients address.
* Each route requires a `path` regular expression (RE2 syntax) that runs against the object key portion after the virtual bucket. Named (`(?P<name>...)`) and numbered captures can be referenced in rewrites.
* `backend` points to a backend entry.
* `rewrite` (optional) is a list of sequential rules:
  * Every rule must include `result`, a template that can reference `$1`, `$2`, … and named captures like `$rest`.
  * When `pattern` is absent, captures come from the route `path` match; when present, the pattern runs on the current key and exposes its own captures.
  * Rules run in order, feeding the output of one step into the next.
* Routing decisions are based on the path regex only; HTTP method and headers are not used for matching today.

If no rewrite rules are provided, the original key (plus any backend `prefix`) is forwarded unchanged.

#### Path parsing and escaping

The router treats the request path as URL-escaped text and only decodes it right before backend calls:

* The incoming object key is derived from `r.URL.EscapedPath()`, so percent-encoded bytes remain escaped during routing.
* Virtual-hosted style:
  * Bucket is taken from the host (including `virtual_hosts` mappings).
  * Object key is the escaped path without the leading `/`.
* Path-style:
  * Bucket is the first path segment and is decoded with `url.PathUnescape`.
  * Object key is the remainder of the escaped path (still percent-encoded).
* Route matching and rewrite rules operate on the escaped key string, so patterns see `%2F`, `%20`, etc.
* Backend keys are built as `backend.prefix + rewritten_key`.
  * For most SDK-backed operations, the router applies a conservative unescape (valid `%XX` sequences are decoded; invalid escapes are preserved as literal `%`). This means `%2F` becomes `/` before the SDK call.
  * Streaming PUT/UploadPart requests and copy-source headers use the rewritten key as-is (still URL-escaped) when building the upstream URL/header. Endpoint templates expect `${key}` to already be URL-encoded and to preserve `/`.

#### Features

`features` is an optional map reserved for future toggles. Flags are currently parsed but not used by runtime logic.

#### Server Configuration

The `server` section (optional) allows tuning of HTTP server behavior:

```yaml
server:
  read_timeout: 30s        # HTTP read timeout (default: 15s)
  write_timeout: 30s       # HTTP write timeout (default: 15s)
  idle_timeout: 60s        # HTTP idle timeout (default: 60s)
  max_body_size: 4GB         # Max request/response body size (default: 4GB)
  route_cache_size: 1k             # LRU cache size for route matching (default: 1000)
```

**Value Formats:**

- **Timeouts** (`read_timeout`, `write_timeout`, `idle_timeout`):
  - Go duration format: `30s`, `2m`, `1h`, `15m` (recommended)
  - Raw seconds (backward compatible): `30`, `60`, `3600`
  - Default units: seconds when using plain numbers

- **Size** (`max_body_size`):
  - With units: `4GB`, `512MB`, `100KB`, `1024B`, `4GiB`, `512MiB`, `100KiB`
  - Raw bytes: `4294967296` (default: 4GB)
  - Supported units: B, KB, MB, GB, KiB, MiB, GiB (case-insensitive)
  - Uses binary units: 1KB = 1024B, 1MB = 1024KB, etc.
  - Binary units: 1KiB = 1024B, 1MiB = 1024KiB, 1GiB = 1024MiB

- **Count** (`route_cache_size`):
  - With units: `1k` (1000), `1m` (1000000)
  - Raw number: `1000` (default: 1000)
  - Supported units: k (thousands), m (millions)

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `read_timeout` | string/int | 15s | HTTP server read timeout. Supports Go duration format (e.g., `30s`, `2m`) or seconds as a number. |
| `write_timeout` | string/int | 15s | HTTP server write timeout. Supports Go duration format or seconds as a number. |
| `idle_timeout` | string/int | 60s | HTTP server idle timeout. Supports Go duration format or seconds as a number. |
| `max_body_size` | string/int | 4GB | Maximum size of request and response bodies. Supports units (GB, MB, KB, B, GiB, MiB, KiB) or raw bytes. Requests exceeding this limit are rejected. |
| `route_cache_size` | string/int | 1000 | LRU cache size for route matching decisions. Supports units (k, m) or raw counts. Larger values improve performance but use more memory. Set to 0 to disable caching. |

#### Authentication Configuration

The `auth` section (optional) controls signature verification behavior:

```yaml
auth:
  default_region: us-east-1         # Default AWS region for SigV4 verification (default: us-east-1)
  clock_skew_leeway: 15m             # Clock skew tolerance in Go duration format (default: 15m)
```

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `default_region` | string | us-east-1 | Default AWS region used when verifying SigV4 signatures. This region is used when the client doesn't explicitly specify a region in the Authorization header. Clients can override this by including a region in their credential scope. |
| `clock_skew_leeway` | string | 15m | Clock skew tolerance as a Go duration string (e.g., `15m`, `900s`, `1h`). Requests with timestamps outside this range from server time are rejected to prevent replay attacks. AWS default is 15 minutes. |

#### Virtual Hosts Configuration

The `virtual_hosts` section (optional) enables S3 virtual-hosted-style requests (bucket names in host headers). It is active when the `hosts` list is non-empty.

```yaml
virtual_hosts:
  hosts:                            # List of hosts - string (wildcard/suffix) or map for bucket mapping
    - api.local: "my-bucket"        # Map: explicit bucket mapping (api.local -> my-bucket)
    - "localhost:8080": "bucket-1"  # Map with explicit port mapping
    - "*.s3.localhost"              # Wildcard: bucket from subdomain (mybucket.s3.localhost -> mybucket)
    - ".example.com"                # Suffix: bucket from subdomain (bucket.example.com -> bucket)
    - "*.custom.local": "default"   # Wildcard with explicit mapping (any.custom.local -> default)
```

**Virtual Host Mode:**
- When a request matches a configured virtual host, the bucket name is determined by the configuration
- **Map entries**: The bucket name is explicitly mapped (e.g., `api.local: "my-bucket"` → bucket `my-bucket`)
- **Wildcard/suffix strings**: The subdomain is extracted as the bucket name (e.g., `mybucket.s3.localhost` → bucket `mybucket`)
- **Wildcards with mapping**: All matching hosts use the explicit bucket name
- **Exact host strings without mapping are ignored** (fall through to path-style resolution)
- This allows S3 clients to use virtual-hosted-style URLs instead of path-style URLs

**Host Matching Rules:**
- Exact matches: `localhost:8080` matches only requests to that exact host:port
- Wildcard matches: `*.example.com` matches `bucket.example.com` but not `foo.bar.example.com`
- Port support: Patterns can include explicit ports; omitting the port matches any port on that host

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `hosts` | array | empty | List of host entries. Each entry can be a wildcard/suffix string or a map with one key-value pair (host: bucket). Supports wildcards and ports. |

## Running s3-router

### Command-line Flags

s3-router supports the following command-line flags:

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `-config` | string | `config.yaml` | Path to configuration file |
| `-credentials` | string | (none) | Path to credentials file (optional) |
| `-listen` | string | `:8080` | Address to listen on for the main S3 API server |
| `-admin` | string | `:9090` | Address to listen on for the admin server |
| `-log-level` | string | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `-log-format` | string | `auto` | Log format: `auto`, `text`, `json`. When `auto`, uses human-friendly text format for terminal output and structured JSON for piped/redirected output |
| `-timeout` | duration | `30s` | Request timeout (e.g., `30s`, `2m`) |
| `-check-route` | string | (none) | Print routing decision for an `s3://bucket/key` and exit |

### Logging

s3-router provides human-friendly logging with automatic format detection:

- **Terminal (interactive)**: Outputs human-readable text format with timestamps and structured key-value pairs
- **Piped/Redirected**: Automatically switches to structured JSON format for easy parsing by log aggregation systems

You can override the automatic detection with the `-log-format` flag:

```bash
# Interactive terminal - auto-detects text format
./s3router -config config.yaml

# Force text format (human-readable)
./s3router -config config.yaml -log-format text

# Force JSON format (structured, for log aggregation)
./s3router -config config.yaml -log-format json
```

### Authentication & Authorization

s3-router implements **SigV4 signature verification** on object operations (GET, PUT, DELETE, etc.). 

**Note:** Bucket-level operations (ListBuckets, CreateBucket, DeleteBucket) bypass authentication and return policy-driven responses for compatibility with S3 clients that expect these operations.

#### Authenticated Operations (Require SigV4 Signature)

All object operations require authentication:

- **Object Operations**: GetObject, PutObject, DeleteObject, HeadObject, CopyObject
- **Multi-part Operations**: CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload, ListParts
- **Listing Operations**: ListObjects, ListObjectsV2  
- **ACL/Tagging Operations**: GetObjectAcl, PutObjectAcl, GetObjectTagging, PutObjectTagging
- **Other Operations**: DeleteObjects, GetObjectAttributes, etc.

**Unauthenticated requests** to these operations are rejected with a 401 Unauthorized response.

#### Unauthenticated Operations (Bypassed for Compatibility)

The following bucket-level operations return fixed responses without requiring authentication:

- **ListBuckets**: Returns the configured virtual buckets (synthetic timestamps)
- **CreateBucket**: Returns `409 BucketAlreadyExists` (no state mutation)
- **DeleteBucket**: Returns `403 BucketNotEmpty` (no state mutation)

These operations are bypassed to support S3 clients that probe for bucket operations during initialization.

#### Static Credential Store

Authentication uses a static credential store loaded from a JSON file mapping access key IDs to secret keys.

**Credentials file format** (`credentials.json`):
```json
{
  "AKIAIOSFODNN7EXAMPLE": {
    "secret_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
    "enabled": true,
    "created_at": "2024-01-01T00:00:00Z"
  },
  "AKIATESTKEYEXAMPLE01": {
    "secret_key": "test_secret_key_12345678901234567890",
    "enabled": true
  }
}
```

#### Configuration

Credentials file can be specified in two ways:

1. **CLI flag** (takes precedence):
   ```bash
   ./s3router -config config.yaml -credentials credentials.json
   ```

2. **Config file** (`config.yaml`):
   ```yaml
   credentials_store: /etc/s3router/credentials.json
   
   backends:
     backend1:
       bucket: my-bucket
   
   buckets:
     - name: virtual-bucket
       routes:
         - path: ^(.*)$
           backend: backend1
   ```

#### Signature Verification

s3-router verifies **AWS SigV4 signatures** on incoming requests, supporting:

- **Header-based signatures**: Authorization header with standard AWS SigV4 format
  ```
  Authorization: AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20240101/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date;x-amz-content-sha256, Signature=abc123...
  X-Amz-Date: 20240101T120000Z
  X-Amz-Content-Sha256: 5d41402abc4b2a76b9719d911017c592
  ```

- **Query-based signatures**: Presigned URLs with X-Amz-Signature parameter
  ```
  GET /?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F...&X-Amz-Date=20240101T120000Z&X-Amz-Signature=...
  ```

##### Multi-Region Support

s3-router automatically extracts the region from the client's credential scope in the Authorization header. This allows clients from any region to use the router:

```yaml
# Client in ap-northeast-1
Authorization: AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20240101/ap-northeast-1/s3/aws4_request, ...

# Client in eu-west-1  
Authorization: AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20240101/eu-west-1/s3/aws4_request, ...
```

If the client doesn't specify a region in the credential scope, the default region from the `auth` config section is used:

```yaml
auth:
  default_region: ap-northeast-1  # Used when client doesn't specify a region
```

#### Security Features

- **Timestamp validation**: Requests with timestamps outside the configured clock skew tolerance (default: ±15m) are rejected to prevent replay attacks. Configure via `auth.clock_skew_leeway` using Go duration format (e.g., `15m`, `900s`).
- **Credential lookup**: Only requests signed with registered access keys are accepted
- **Constant-time comparison**: Signature comparison uses constant-time HMAC comparison to prevent timing attacks
- **Disabled credentials**: Credentials can be marked `"enabled": false` in the JSON file to revoke access

#### Error Responses

Authentication errors return appropriate HTTP status codes:

| Status | Error Code | Reason |
|--------|-----------|--------|
| 401 | MissingAuthenticationToken | No Authorization header or X-Amz-Signature |
| 400 | InvalidAuthHeader | Malformed or missing authorization components |
| 403 | InvalidAccessKeyId | Access key not found in credential store |
| 403 | SignatureDoesNotMatch | Signature verification failed |
| 403 | RequestTimeTooSkewed | Request timestamp too old or too far in future |

#### Using with AWS SDK / CLI

To test s3-router with AWS CLI or SDK:

```bash
# AWS CLI - configure credentials and endpoint
aws configure --profile s3router
# AWS Access Key ID: AKIAIOSFODNN7EXAMPLE
# AWS Secret Access Key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
# Default region: us-east-1

# Test with AWS CLI
aws --profile s3router --endpoint-url http://localhost:8080 s3 ls s3://virtual-bucket/

# Python boto3 example
import boto3
client = boto3.client(
    's3',
    endpoint_url='http://localhost:8080',
    aws_access_key_id='AKIAIOSFODNN7EXAMPLE',
    aws_secret_access_key='wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY',
    region_name='us-east-1'
)
response = client.list_objects_v2(Bucket='virtual-bucket')
```

#### Credential Management Best Practices

##### For Client Authentication Credentials (credentials_store)

1. **File permissions**: Restrict credentials file to user/group that runs s3-router
   ```bash
   chmod 600 credentials.json
   ```

2. **Secrets management**: Use environment variable substitution or secret management tools
   ```bash
   export ROUTER_CREDENTIALS_PATH=/run/secrets/s3router_credentials.json
   ./s3router -config config.yaml -credentials $ROUTER_CREDENTIALS_PATH
   ```

3. **Credential rotation**: Update credentials.json without restarting (restart currently required)
   ```bash
   kill <pid>
   ./s3router -config config.yaml -credentials credentials.json
   ```

4. **Audit logging**: Enable debug logging to see authentication attempts
   ```bash
   ./s3router -config config.yaml -credentials credentials.json -log-level debug
   ```

##### For Backend Credentials

**Recommended approach: Use externalized credential sources instead of inline credentials**

1. **Use `credentials` field with externalized sources**
   - File-based: Mount secrets from Kubernetes, Docker secrets, or configuration management
   - AWS Secrets Manager: Automatically rotated, centrally managed, IAM-controlled
   - Default chain: Leverage IAM roles on EC2/ECS, environment variables, or shared credentials

2. **File-based credentials (`type: file`)**
   ```bash
   # Use restricted permissions
   chmod 600 /etc/s3router/backend-creds.json
   
   # Mount via Docker/Kubernetes for easy rotation
   docker run -v /secrets/backend-creds.json:/etc/s3router/backend-creds.json ...
   ```

3. **AWS Secrets Manager (`type: aws-secrets-manager`)**
   ```yaml
   credentials:
     type: aws-secrets-manager
     secret_name: prod/s3-router/backend1
     region: us-east-1
   ```
   
   Benefits:
   - Automatic credential rotation via Lambda
   - Centralized secret management
   - IAM-based access control
   - Audit trail via CloudTrail
   - No plaintext credentials in config files

4. **Default AWS Chain (`type: default`)**
   ```yaml
   credentials:
     type: default
   ```
   
   Best for:
   - EC2 instances with IAM roles
   - ECS tasks with IAM task roles
   - Kubernetes pods with IRSA (IAM Roles for Service Accounts)
   - Development with `~/.aws/credentials`

5. **STS Role Assumption for Cross-Account Access**
   ```yaml
   credentials:
     type: file
     path: /etc/s3router/base-creds.json
     assume_role:
       role_arn: arn:aws:iam::ACCOUNT2:role/S3RouterRole
       duration: 3600
   ```
   
   Use for:
   - Cross-account bucket access
   - Least-privilege access (assume role with minimal permissions)
   - Audit trails (STS calls are logged in CloudTrail)

## Prefix Optimization

s3-router includes an automatic prefix optimization feature that significantly improves performance for patterns with static prefixes. When a route pattern contains a static prefix (e.g., `^folder/(.*)$`), the router extracts this prefix and uses it in S3 ListObjects API calls instead of scanning all objects in the bucket.

### How It Works

1. **Pattern Analysis**: Routes are analyzed to detect static prefixes at the beginning of the pattern
2. **Prefix Extraction**: Static prefixes are extracted and used directly in S3 ListObjects queries with the `prefix` parameter
3. **Automatic Filtering**: Only objects matching the prefix are retrieved, reducing API calls and improving performance

### Example

```yaml
backends:
  backend1:
    bucket: my-bucket

buckets:
  - name: data
    routes:
      - path: ^logs/(?P<rest>.*)$    # Static prefix "logs/"
        backend: backend1
```

With this configuration:
- Instead of listing all objects in `my-bucket`, the router queries for objects with prefix `logs/`
- This reduces the result set, improves performance, and lowers S3 API costs
- The router then applies the regex pattern to the filtered results

### Requirements for Optimization

Prefix optimization applies when:
- The pattern starts with literal characters (e.g., `^logs/`)
- The remainder uses only wildcard patterns (e.g., `.*`, `.+`)
- No complex regex constraints follow the static prefix

If a pattern cannot be optimized (e.g., complex regex after the prefix), the router falls back to full bucket scans.

## Streaming and Performance Considerations

### PUT Object Streaming

s3-router optimizes PutObject operations for streaming to minimize memory usage:

#### Streaming Mode (No Checksum Trailers)

When a client sends a PUT request **without** checksum trailers (the common case):
- Request body is streamed directly to the S3 backend **without buffering**
- Memory usage: Constant, regardless of object size
- Best for: Large file uploads

**Typical clients and scenarios:**
- AWS CLI: `aws s3 cp file.bin s3://bucket/path/` ✅ Streamed
- AWS SDK v3 (JavaScript/Python): Standard PUT requests ✅ Streamed
- S3 multipart uploads with non-trailer checksums ✅ Streamed

#### Buffering Mode (With Checksum Trailers)

When a client sends a PUT request **with** checksum trailers (e.g., `x-amz-trailer: x-amz-checksum-sha256`):
- Entire request body must be buffered to read trailers at the end
- Memory usage: = Object size (pre-calculated via Content-Length, or full body if not provided)
- Required because trailers arrive after the body in chunked encoding

**Clients that use checksum trailers:**
- AWS SDK v3 with flexible checksums enabled
- S3Transfer clients with checksum_algorithm configured
- Some S3-compatible clients that support trailer checksums

### Memory Management

For environments with memory constraints:

1. **Verify client doesn't require checksum trailers**
   - Most clients don't send trailers by default
   - Check if client explicitly requests trailer checksums

2. **Configure max_body_size appropriately**
   ```yaml
   server:
     max_body_size: 5GB  # Limit memory usage in checksum trailer mode
   ```

3. **Monitor memory usage**
   - In checksum trailer mode, peak memory = largest object size
   - Use `-log-level debug` to see which operations buffer trailers

4. **Recommended approach: Upgrade clients to send checksums in headers**
   - Avoids trailer buffering entirely
   - Example (AWS SDK): Configure flexible checksums with headers-only mode

### Performance Tips

- **Disable route caching if not needed**
  ```yaml
  server:
    route_cache_size: 0
  ```

- **Tune connection pooling for backend clients**
  ```yaml
  backends:
    backend1:
      timeout: 30s
      retries: 3
  ```

- **Monitor circuit breaker activation**
  - Circuit breaker automatically isolates failing backends
  - Triggered when failure rate ≥ 60% over ≥3 requests
  - Auto-heals after 10 seconds
