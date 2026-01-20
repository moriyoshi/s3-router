# Helm Integration Tests

Integration tests for the s3-router Helm chart using kind (Kubernetes in Docker) cluster.

## Quick Start

Build the s3-router binary first:
```bash
make build
```

Then run tests:
```bash
cd tests/helm-integration
make test
```

## Prerequisites

### Required

- **Docker**: Container runtime
  ```bash
  docker --version
  ```

- **kind**: Kubernetes in Docker (creates local K8s clusters)
  ```bash
  brew install kind  # macOS
  # or visit https://kind.sigs.k8s.io/docs/user/quick-start/
  ```

- **helm**: Kubernetes package manager
  ```bash
  brew install helm  # macOS
  # or visit https://helm.sh/docs/intro/install/
  ```

- **Python 3.9+**: For test execution
  ```bash
  python3 --version
  ```

### Optional

- **kubectl**: For manual debugging (installed separately or via kind)
- **make**: For convenient test commands

## Test Files

- **test_helm_deployment.py** - Core Helm deployment tests (7 tests)
- **test_integration_ported.py** - S3 operation tests via Kubernetes (optional)
- **conftest.py** - Pytest configuration and fixtures

## Running Tests

### All Tests

```bash
make test
```

### Verbose Output

```bash
make test-verbose
```

### Single Test

```bash
make test-single
```

### Specific Test

```bash
uv run pytest test_helm_deployment.py::TestHelmDeployment::test_deployment_exists -vv
```

### From Repository Root

```bash
make helm-integration-test
```

## Test Coverage

### TestHelmDeployment (7 tests)

| Test | Validates |
|------|-----------|
| `test_deployment_exists` | Deployment creation with correct replicas |
| `test_pods_are_ready` | Pods running and containers ready |
| `test_service_exists` | Service creation with correct ports |
| `test_configmap_created` | Configuration loaded from ConfigMap |
| `test_service_account_created` | RBAC setup with ServiceAccount |
| `test_pod_port_forwarding` | Port exposure (8080, 9090) |
| `test_deployment_replicas_match` | Replica availability and consistency |

### TestPodHealth (optional)

- Pod resource limits (CPU/memory)
- Security context (non-root execution)
- No privileged containers

## Architecture

### Components

```
Host Machine (Docker Engine)
│
├─ Kind Cluster (Kubernetes in Docker)
│  ├─ Kubernetes API Server
│  ├─ Helm Chart Deployment
│  │  └─ s3-router Pods
│  │     ├─ Port 8080 (S3 API)
│  │     ├─ Port 9090 (Admin)
│  │     └─ ConfigMap/ServiceAccount
│  └─ Service for Pod Access
│
├─ Moto Pod (optional - S3 mock)
│  └─ S3 Mock Service (port 5000)
│
└─ Test Suite (Python/pytest)
   └─ Kubernetes Python Client
```

### Fixture Hierarchy (Session-Scoped)

```
setup_kind_cluster
├── kube_config_context (load kubeconfig)
├── api_client & v1_api (K8s API clients)
├── test_namespace (create isolated namespace)
├── moto_pod (optional S3 mock)
├── moto_service (optional S3 service endpoint)
└── _helm_chart_deployed (deploy and wait)
```

## Configuration

### test-values.yaml

Helm overrides for testing:

```yaml
replicaCount: 1                    # Single replica for testing
image:
  pullPolicy: IfNotPresent        # Pull from registry
  tag: "latest"

resources:
  limits:
    cpu: 200m
    memory: 256Mi
  requests:
    cpu: 50m
    memory: 64Mi

config:
  backends:
    test-backend:
      endpoint: http://moto:5000   # Optional moto endpoint
      bucket: test-bucket
```

## Troubleshooting

### Kind cluster fails to create

```bash
# Check docker is running
docker ps

# Check kind installation
kind version

# Remove old clusters if stuck
kind delete cluster --name s3-router-test
```

### Pods not becoming ready

```bash
# Check pod details
kubectl describe pod -n s3-router-test

# Check pod logs
kubectl logs -n s3-router-test <pod-name>

# Check events
kubectl get events -n s3-router-test
```

### Helm deployment fails

```bash
# Check Helm chart
helm lint ../../chart

# Check helm release status
helm status s3-router-test -n s3-router-test

# Check helm install logs
helm get manifest s3-router-test -n s3-router-test
```

### Test timeout

```bash
# Increase timeout
export PYTEST_TIMEOUT=600
make test
```

### Port conflicts

```bash
# Kind uses random ports. Check for issues:
docker ps | grep s3-router-test
```

## Adding New Tests

### Basic test structure

```python
def test_my_feature(v1_api, test_namespace, helm_chart_deployed):
    """Test my feature."""
    # Query Kubernetes API
    result = v1_api.list_namespaced_pod(test_namespace)
    
    # Assert expected conditions
    assert len(result.items) > 0
```

### Testing S3 operations

```python
def test_s3_put_object(s3_client):
    """Test putting object through s3-router."""
    s3_client.put_object(
        Bucket='test-bucket',
        Key='test-key',
        Body=b'test-data'
    )
    obj = s3_client.get_object(Bucket='test-bucket', Key='test-key')
    assert obj['Body'].read() == b'test-data'
```

## Performance

- **Setup**: ~20-30s (kind cluster creation)
- **Build**: ~5-10s (docker image build/load)
- **Deploy**: ~10-20s (helm install + pod readiness)
- **Tests**: ~10-20s (Kubernetes API calls)
- **Total**: ~60-90s

## Cleanup

### Manual cleanup

```bash
make clean                          # Remove test artifacts
kind delete cluster --name s3-router-test  # Remove cluster
```

### Automatic cleanup

- Namespaces are automatically deleted when `no_cleanup_cluster=false` (default)
- Kind cluster persists for faster re-runs; delete manually if needed
- All test cleanup happens automatically

## Key Differences from Previous Version

- **Kind-based**: Uses kind clusters instead of docker-compose + minikube
- **Simpler setup**: No need for docker-compose or external services
- **Faster startup**: Kind clusters are lightweight
- **Better isolation**: Each test session has its own namespace
- **Optional moto**: S3 mocking is optional, not required

## Files

| File | Purpose |
|------|---------|
| `conftest.py` | Pytest configuration and fixtures |
| `test_helm_deployment.py` | Deployment and pod tests |
| `test_integration_ported.py` | S3 operation tests (optional) |
| `test-values.yaml` | Helm chart overrides |
| `requirements.txt` | Python dependencies |
| `Makefile` | Common test commands |
| `README.md` | This file |

## References

- [kind Documentation](https://kind.sigs.k8s.io/)
- [Helm Documentation](https://helm.sh/docs/)
- [Kubernetes Python Client](https://github.com/kubernetes-client/python)
- [Pytest Documentation](https://docs.pytest.org/)
