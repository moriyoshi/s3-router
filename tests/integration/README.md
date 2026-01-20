# S3 Router Integration Tests

Integration tests for s3-router using moto (AWS S3 mock) and a real s3-router binary instance.

## Overview

These tests verify s3-router's functionality by:
1. Starting a moto S3 mock server
2. Launching s3-router with configuration pointing to moto
3. Running HTTP requests against the s3-router to validate behavior

The tests don't require Docker or live AWS services, making them fast and deterministic.

### Test Files

- **test_s3_operations.py** - Core S3 operations (CRUD, metadata, listings, error handling)
- **test_routing.py** - Router logic (routing decisions, path rewrites, backend selection)
- **test_router_e2e.py** - End-to-end router behavior via HTTP requests
- **test_resilience.py** - Error handling, timeouts, circuit breaker behavior
- **test_advanced_features.py** - Advanced S3 features (encryption, ACLs, caching)

## Requirements

`uv` has to be installed.

> **Note:** The s3router binary is automatically built to `tests/integration/bin/` when tests start. No manual build step is required.

## Running Tests

### Run all integration tests
```bash
uv run pytest tests/integration/ -v
```

### Run specific test file
```bash
uv run pytest tests/integration/test_s3_operations.py -v
uv run pytest tests/integration/test_routing.py -v
uv run pytest tests/integration/test_router_e2e.py -v
```

### Run specific test class
```bash
uv run pytest tests/integration/test_s3_operations.py::TestS3CRUDOperations -v
```

### Run specific test function
```bash
uv run pytest tests/integration/test_s3_operations.py::TestS3CRUDOperations::test_put_get_object -v
```

### Run tests by keyword
```bash
uv run pytest tests/integration/ -k "routing" -v
uv run pytest tests/integration/ -k "concurrent or large" -v
```

### Run with coverage
```bash
uv run pytest tests/integration/ --cov=pkg --cov=internal --cov-report=html
```

## How Tests Work

### Test Flow

1. **s3router_with_moto fixture** - Automatically builds s3-router binary to `tests/integration/bin/` (only if not already built), then starts an instance with YAML config pointing to moto
2. **moto_server fixture** - Starts a mock S3 service on a random port
3. **Tests make HTTP requests** - Tests use HTTP requests to validate router behavior
4. **Cleanup** - Both servers stop automatically when fixtures are torn down

### Example Test

```python
import boto3
import pytest

def test_simple_routing(s3router_with_moto):
    """Test basic routing through s3-router."""
    # Create boto3 client pointing to the router
    client = boto3.client(
        's3',
        endpoint_url=s3router_with_moto['router_url'],
        region_name='us-east-1',
        aws_access_key_id='testing',
        aws_secret_access_key='testing',
    )
    
    # Make request through router
    client.put_object(Bucket='test-bucket', Key='test-key', Body=b'test data')
    resp = client.get_object(Bucket='test-bucket', Key='test-key')
    assert resp['Body'].read() == b'test data'
```

## Test Coverage

### S3 Operations (test_s3_operations.py)
- PUT, GET, DELETE objects
- HEAD operations
- Large file handling
- Metadata preservation
- Content-Type handling
- ETag tracking
- List operations with prefixes
- Error handling for non-existent objects

### Router Logic (test_routing.py)
- Prefix-based routing
- Conditional routing (HTTP methods)
- Path rewriting with regex
- Route ordering (first match wins)
- Multi-backend scenarios
- Backend isolation

### End-to-End (test_router_e2e.py)
- Full HTTP request/response cycles
- Virtual bucket operations
- Signature validation
- Error responses

### Resilience (test_resilience.py)
- Circuit breaker behavior
- Timeout handling
- Error recovery
- Concurrent operations

### Advanced Features (test_advanced_features.py)
- Server-side encryption
- ACLs
- Caching headers
- Multipart uploads

## Performance

- **Startup**: ~1-2 seconds (servers start, no Docker)
- **Per test**: ~100-200ms average
- **Full suite**: ~30-60 seconds total (varies by system)
- **No Docker required**: Runs anywhere with Go and Python

## Key Fixtures

All fixtures are defined in `conftest.py`:

- `moto_server` - Pytest yield fixture for moto S3 server (yields endpoint URL)
- `s3router_with_moto` - Pytest yield fixture for s3-router instance (yields dict with router_url, admin_url, log_file, moto_endpoint)
- `moto_client` - Pre-configured boto3 client pointing to moto
- `router_client` - Pre-configured boto3 client pointing to the router

## Adding New Tests

### Basic test using fixtures

```python
import boto3
import pytest

def test_my_feature(s3router_with_moto, moto_client):
    """Test my feature."""
    # Create bucket on moto
    moto_client.create_bucket(Bucket='my-bucket')
    
    # Create client pointing to router
    client = boto3.client(
        's3',
        endpoint_url=s3router_with_moto['router_url'],
        region_name='us-east-1',
        aws_access_key_id='testing',
        aws_secret_access_key='testing',
    )
    
    # Your test code
    client.put_object(Bucket='my-bucket', Key='key', Body=b'data')
```

## Troubleshooting

### Port already in use

```bash
# Ports are auto-selected, but check for zombie processes
lsof -i :8080
lsof -i :9999  # moto default
```

### Import errors

```bash
# Install dependencies
pip install -r requirements.txt
```

### Moto server fails to start

```bash
# Ensure boto3 and moto are installed
pip install --upgrade moto boto3
```

## Continuous Integration

Tests run in GitHub Actions via `.github/workflows/ci.yml`.

## References

- [Moto Documentation](https://docs.getmoto.org/)
- [Boto3 Documentation](https://boto3.amazonaws.com/v1/documentation/api/latest/index.html)
- [Pytest Documentation](https://docs.pytest.org/)
- [S3 API Reference](https://docs.aws.amazon.com/s3/latest/API/)
