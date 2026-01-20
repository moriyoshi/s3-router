# S3 Router Helm Chart

A Helm chart for deploying S3 Router, an S3 API-compatible HTTP server with flexible routing rules.

## Features

- **Secure credential management**: Credentials stored in Kubernetes Secrets
- **Flexible routing configuration**: Full routing rules rendered as ConfigMaps
- **Multi-backend support**: Route to multiple S3 backends with different credentials
- **Security-first**: Runs as non-root user with read-only filesystem
- **Production-ready**: Includes health checks, resource limits, and autoscaling

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- Docker image for s3-router available in your registry

## Installation

### Add Helm Repository (if applicable)

```bash
helm repo add s3-router https://example.com/charts
helm repo update
```

### Install the Chart

```bash
helm install my-s3-router ./chart -f values.yaml
```

### Install with Custom Values

```bash
helm install my-s3-router ./chart \
  -f chart/values.yaml \
  --set config.backends.mybackend.endpoint=s3.amazonaws.com \
  --set credentials.mybackend.access_key_id=AKIA... \
  --set credentials.mybackend.secret_access_key=...
```

## Configuration

### Key Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of pod replicas | `1` |
| `image.repository` | Docker image repository | `s3-router` |
| `image.tag` | Docker image tag | Chart appVersion |
| `service.type` | Kubernetes service type | `ClusterIP` |
| `service.port` | Service port | `80` |
| `config.backends` | S3 backend configurations (with credentials) | `{}` |
| `config.buckets` | Virtual bucket routing rules | `[]` |
| `credentials` | Client authentication credentials | `{}` |
| `virtualHosts.enabled` | Enable virtual host-style addressing | `false` |
| `virtualHosts.hosts` | List of virtual host patterns | `[]` | |

### Configuring Backends

Backends define S3 endpoints and bucket information with their credentials. Add backends to `config.backends`:

```yaml
config:
  backends:
    # Using AWS SDK default credential chain (recommended for EKS with IRSA)
    backend1:
      endpoint: s3.us-east-1.amazonaws.com
      bucket: my-bucket-1
      prefix: prefix1/
      credentials:
        type: default
    
    # Using inline credentials (stored in Kubernetes Secret automatically)
    backend2:
      endpoint: s3.eu-west-1.amazonaws.com
      bucket: my-bucket-2
      prefix: prefix2/
      credentials:
        type: inline
        access_key_id: "AKIA..."
        secret_access_key: "..."
        session_token: ""  # optional
    
    # Using file-based credentials
    backend3:
      bucket: my-bucket-3
      credentials:
        type: file
        path: /etc/s3router/custom-creds.json
    
    # Using AWS Secrets Manager
    backend4:
      bucket: my-bucket-4
      credentials:
        type: aws-secrets-manager
        secret_name: s3-router/backend4-credentials
        region: us-west-2
    
    # With role assumption
    backend5:
      bucket: my-bucket-5
      credentials:
        type: default
        assume_role:
          role_arn: arn:aws:iam::123456789:role/S3RouterRole
          session_name: s3-router-backend5
          duration: 3600
```

#### Credential Types

| Type | Description |
|------|-------------|
| `default` | Uses AWS SDK default credential chain (env vars, shared credentials, IMDS/IRSA) |
| `inline` | Static credentials specified in values (automatically stored in K8s Secret) |
| `file` | Credentials loaded from a JSON file |
| `aws-secrets-manager` | Credentials fetched from AWS Secrets Manager |

**Security Note**: When using `type: inline`, credentials are automatically extracted from the values and stored in a Kubernetes Secret. The config references the secret file path instead of embedding credentials directly.

### Configuring Client Credentials

The `credentials` section defines credentials for clients connecting TO s3-router (not backend credentials). These are used for SigV4 request authentication:

```yaml
credentials:
  AKIAIOSFODNN7EXAMPLE:
    secret_key: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
    enabled: true
    created_at: "2024-01-01T00:00:00Z"
  
  AKIATESTKEYEXAMPLE01:
    secret_key: "test_secret_key_12345678901234567890"
    enabled: true
    created_at: "2024-01-01T00:00:00Z"
```

These credentials are stored in a Kubernetes Secret and mounted at `/etc/s3router/credentials/credentials.json`.

**Important**: Use a secret management tool (e.g., Sealed Secrets, External Secrets, or helm-secrets) for production environments.

### Configuring Routing Rules

Define virtual buckets and routing rules in `config.buckets`:

```yaml
config:
  buckets:
    - name: my-virtual-bucket
      routes:
        - path: ^foo/(?P<rest>.*)
          backend: backend1
          rewrite:
            - result: $rest
        
        - path: ^bar/.*
          backend: backend2
          rewrite:
            - pattern: ^bar/special/(.*)
              result: SPECIAL/$1
            - pattern: ^bar/(?P<patternInRewriter>.*)
              result: $patternInRewriter
```

### Configuring Virtual Hosts

Enable virtual host-style bucket addressing (bucket name in hostname instead of path):

```yaml
virtualHosts:
  enabled: true
  hosts:
    # Simple host matching
    - "localhost"                    # bucket = "localhost"
    - "localhost:8080"               # bucket = "localhost:8080"
    
    # Explicit bucket mapping
    - api.local: "my-bucket"         # Host "api.local" → bucket "my-bucket"
    
    # Wildcard patterns (bucket extracted from subdomain)
    - "*.s3.localhost"               # "mybucket.s3.localhost" → bucket "mybucket"
    - "*.api.example.com:8080"       # "data.api.example.com:8080" → bucket "data"
    
    # Wildcard with explicit bucket
    - "*.custom.local": "default-bucket"  # Any subdomain → bucket "default-bucket"
```

#### Virtual Host Patterns

| Pattern | Example Host | Bucket Name |
|---------|--------------|-------------|
| `"localhost"` | `localhost` | `localhost` |
| `api.local: "my-bucket"` | `api.local` | `my-bucket` |
| `"*.s3.localhost"` | `data.s3.localhost` | `data` |
| `"*.s3.local": "shared"` | `any.s3.local` | `shared` |

### Rendering Configuration to YAML

The chart automatically renders all configuration from `values.yaml` into a YAML file in the ConfigMap. View it with:

```bash
kubectl get configmap <release>-s3-router-config -o yaml
kubectl get configmap <release>-s3-router-config -o jsonpath='{.data.config\.yaml}' | yq -
```

## How Configuration is Managed

### ConfigMap (Routing Rules)

The complete routing configuration is rendered as YAML in a ConfigMap named `<release>-s3-router-config`:

- Contains `config.yaml` with all backend endpoints and routing rules
- Mounted as read-only at `/etc/s3router/config.yaml`
- Updated on Helm release upgrades

### Secret (Credentials)

Credentials are stored in a Secret named `<release>-s3-router-credentials`:

- **Client credentials** (`credentials.json`): For authenticating clients connecting to s3-router
- **Backend credentials** (`backend-<name>.json`): For backends using `type: inline` credentials

The secret structure:
```
credentials.json          # Client authentication credentials
backend-backend1.json     # Inline credentials for backend1 (if type: inline)
backend-backend2.json     # Inline credentials for backend2 (if type: inline)
```

Mounted volumes:
- `/etc/s3router/credentials/` - Client credentials
- `/etc/s3router/backend-credentials/` - Backend inline credentials (only if any backend uses `type: inline`)

### Pod Checksum Annotations

The Deployment includes checksums of ConfigMap and Secret to automatically restart pods when configuration changes:

```yaml
annotations:
  checksum/config: <hash>
  checksum/secret: <hash>
```

## Usage Examples

### Example 1: Simple Single Backend

```yaml
config:
  backends:
    aws:
      endpoint: s3.amazonaws.com
      bucket: my-bucket
      prefix: data/
      credentials:
        type: inline
        access_key_id: "AKIA..."
        secret_access_key: "..."
  
  buckets:
    - name: my-bucket
      routes:
        - path: ^(.*)
          backend: aws
          rewrite:
            - result: $1
```

### Example 2: Multi-Region Setup with IRSA

```yaml
config:
  backends:
    us-east:
      endpoint: s3.us-east-1.amazonaws.com
      bucket: us-bucket
      prefix: data/
      credentials:
        type: default  # Uses IRSA on EKS
    
    eu-west:
      endpoint: s3.eu-west-1.amazonaws.com
      bucket: eu-bucket
      prefix: data/
      credentials:
        type: default
        assume_role:
          role_arn: arn:aws:iam::123456789:role/EUAccessRole
  
  buckets:
    - name: global-bucket
      routes:
        - path: ^us/(?P<rest>.*)
          backend: us-east
          rewrite:
            - result: $rest
        
        - path: ^eu/(?P<rest>.*)
          backend: eu-west
          rewrite:
            - result: $rest
```

### Example 3: MinIO + AWS

```yaml
config:
  backends:
    local-minio:
      endpoint: minio.local:9000
      bucket: local-bucket
      prefix: staging/
      credentials:
        type: inline
        access_key_id: "minioadmin"
        secret_access_key: "minioadmin"
    
    aws-prod:
      endpoint: s3.amazonaws.com
      bucket: prod-bucket
      prefix: production/
      credentials:
        type: aws-secrets-manager
        secret_name: prod/s3-credentials
        region: us-east-1
  
  buckets:
    - name: app-bucket
      routes:
        - path: ^staging/(?P<rest>.*)
          backend: local-minio
          rewrite:
            - result: $rest
        
        - path: ^prod/(?P<rest>.*)
          backend: aws-prod
          rewrite:
            - result: $rest
```

## Autoscaling

Enable horizontal pod autoscaling:

```bash
helm install my-s3-router ./chart \
  --set autoscaling.enabled=true \
  --set autoscaling.minReplicas=2 \
  --set autoscaling.maxReplicas=10 \
  --set autoscaling.targetCPUUtilizationPercentage=70
```

## Ingress Configuration

Enable ingress to expose s3-router outside the cluster:

```yaml
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  hosts:
    - host: s3-router.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: s3-router-tls
      hosts:
        - s3-router.example.com
```

## OpenTelemetry Configuration

S3-router includes built-in OpenTelemetry support for distributed tracing and metrics collection. Configure it via the `opentelemetry` section in values.yaml.

### Basic Setup

```yaml
opentelemetry:
  enabled: true
  exporter:
    endpoint: "http://jaeger:4318"  # OTLP HTTP endpoint
    timeout: "10s"
  traces:
    exporter: "otlp"
    sampler: "always_on"            # Options: always_on, always_off, traceidratio
    samplerArg: "1.0"               # For traceidratio: 0-1 (e.g., 0.1 for 10% sampling)
  metrics:
    exporter: "otlp"
  service:
    name: "s3-router"
    version: "0.1.0"
  sdk:
    disabled: false
```

### Jaeger Integration (Local Development)

```bash
# Start Jaeger
helm repo add jaegertracing https://jaegertracing.github.io/helm-charts
helm install jaeger jaegertracing/jaeger \
  --set collector.service.ports.otlp-grpc=4317 \
  --set collector.service.ports.otlp-http=4318

# Deploy s3-router with OTel
helm install my-s3-router ./chart \
  --set opentelemetry.enabled=true \
  --set opentelemetry.exporter.endpoint="http://jaeger-jaeger-collector:4318"

# Access Jaeger UI
kubectl port-forward svc/jaeger-jaeger 16686:16686
# Open http://localhost:16686
```

### OpenTelemetry Collector Integration

```bash
# Deploy OTel Collector with OTLP receiver
helm install otel-collector open-telemetry/opentelemetry-collector

# Deploy s3-router
helm install my-s3-router ./chart \
  --set opentelemetry.enabled=true \
  --set opentelemetry.exporter.endpoint="http://otel-collector-opentelemetry-collector:4318"
```

### Production Setup with Sampling

```yaml
opentelemetry:
  enabled: true
  exporter:
    endpoint: "http://otel-collector:4318"
    timeout: "10s"
  traces:
    exporter: "otlp"
    sampler: "traceidratio"
    samplerArg: "0.1"     # Sample 10% of traces to reduce volume
  metrics:
    exporter: "otlp"
  service:
    name: "s3-router"
    version: "0.1.0"
  sdk:
    disabled: false
```

### Disable OpenTelemetry

```yaml
opentelemetry:
  enabled: false  # Set to false to disable tracing
```

## Monitoring and Debugging

### Check Deployment Status

```bash
kubectl get deployment -l app.kubernetes.io/name=s3-router
kubectl get pods -l app.kubernetes.io/name=s3-router
```

### View Logs

```bash
kubectl logs -f deployment/my-s3-router-s3-router
```

### Access Admin Endpoint

```bash
kubectl port-forward svc/my-s3-router-s3-router 9090:9090
curl http://localhost:9090/health
```

### View Traces

```bash
# Port-forward to Jaeger UI (if using Jaeger)
kubectl port-forward svc/jaeger-jaeger 16686:16686

# Open http://localhost:16686 and search for s3-router traces
```

### Check Configuration

```bash
kubectl get configmap my-s3-router-s3-router-config -o yaml
```

### Check Credentials

```bash
kubectl get secret my-s3-router-s3-router-credentials -o yaml
```

### Check OTel Environment Variables

```bash
kubectl describe pod -l app.kubernetes.io/name=s3-router
# Look for OTEL_* environment variables in the output
```

## Uninstalling the Chart

```bash
helm uninstall my-s3-router
```

## Security Considerations

1. **Credentials**: Store credentials in a secret management tool before deploying to production
2. **Secret Backend**: Consider using Sealed Secrets or External Secrets Operator
3. **RBAC**: Restrict access to ConfigMaps and Secrets using Kubernetes RBAC
4. **Network Policy**: Use NetworkPolicy to restrict traffic to/from s3-router pods
5. **Pod Security**: Chart enforces non-root user and read-only filesystem by default

## Troubleshooting

### Pod won't start

```bash
kubectl describe pod <pod-name>
kubectl logs <pod-name>
```

### Configuration not updated

- ConfigMap changes require pod restart (handled automatically via checksum annotations)
- Verify ConfigMap: `kubectl get configmap <release>-s3-router-config -o yaml`

### Connection issues

- Check service: `kubectl get svc <release>-s3-router`
- Verify endpoints: `kubectl get endpoints <release>-s3-router`
- Test connectivity: `kubectl run -it --rm debug --image=curlimages/curl -- sh`

## License

Same as s3-router project

---

## Quick Start Guide

## Overview

This Helm chart deploys S3 Router with the following key features:

- **Secrets Management**: Credentials stored in Kubernetes Secrets, separate from configuration
- **ConfigMaps**: Routing rules and backend configuration in ConfigMaps
- **Security**: Non-root user, read-only filesystem, security context hardening
- **Production-Ready**: Health checks, resource limits, autoscaling, and pod restart on config changes

## Installation

### 1. Minimal Installation

```bash
helm install s3-router ./chart
```

### 2. With Example Configuration

```bash
helm install s3-router ./chart -f chart/values-example.yaml
```

### 3. With Custom Values

Create a `my-values.yaml` file:

```yaml
image:
  repository: my-registry/s3-router
  tag: v1.0.0

replicaCount: 3

config:
  backends:
    my-backend:
      endpoint: s3.amazonaws.com
      bucket: my-bucket
      prefix: data/
  
  buckets:
    - name: virtual-bucket
      routes:
        - path: ^(.*)
          backend: my-backend
          rewrite:
            - result: $1

credentials:
  my-backend:
    access_key_id: "AKIA..."
    secret_access_key: "..."
```

Then install:

```bash
helm install s3-router ./chart -f my-values.yaml
```

## Architecture

### Configuration Flow

```
values.yaml
    ↓
    ├─→ ConfigMap: config.yaml (backends + buckets + features)
    └─→ Secret: credentials (access keys)
         ↓
      Pod: mounts both
         ↓
      s3router binary reads /etc/s3router/config.yaml
```

### Kubernetes Resources Created

| Resource | Name Pattern | Purpose |
|----------|--------------|---------|
| Deployment | `<release>-s3-router` | Main application pods |
| Service | `<release>-s3-router` | Internal/External access |
| ConfigMap | `<release>-s3-router-config` | Routing configuration (config.yaml) |
| Secret | `<release>-s3-router-credentials` | AWS/S3 credentials |
| ServiceAccount | `<release>-s3-router` | Pod identity (optional) |
| HPA | `<release>-s3-router` | Auto-scaling (optional) |
| Ingress | `<release>-s3-router` | External routing (optional) |

## Common Tasks

### Update Configuration

Edit your values file and upgrade:

```bash
helm upgrade s3-router ./chart -f my-values.yaml
```

Pods will automatically restart due to checksum annotations.

### View Current Configuration

```bash
# See all resources
kubectl get all -l app.kubernetes.io/name=s3-router

# View rendered config
kubectl get configmap -l app.kubernetes.io/name=s3-router -o yaml

# View secrets (base64-encoded)
kubectl get secret -l app.kubernetes.io/name=s3-router -o yaml
```

### Access S3 Router

```bash
# Port-forward to local machine
kubectl port-forward svc/s3-router 8080:80

# Test S3 API
aws s3 ls --endpoint-url http://localhost:8080 --no-sign-request
```

### Check Admin Endpoint

```bash
kubectl port-forward svc/s3-router 9090:9090
curl http://localhost:9090/health
```

### View Logs

```bash
kubectl logs -f deployment/s3-router
kubectl logs -f -l app.kubernetes.io/name=s3-router
```

## Values Reference

### Image Configuration

```yaml
image:
  repository: s3-router        # Docker image
  tag: latest                  # Image tag
  pullPolicy: IfNotPresent     # Pull policy
```

### Service Configuration

```yaml
service:
  type: ClusterIP              # ClusterIP, NodePort, or LoadBalancer
  port: 80                     # External port
  targetPort: 8080             # Container port
  adminPort: 9090              # Admin endpoint port
```

### Replica & Scaling

```yaml
replicaCount: 1                # Initial replicas

autoscaling:
  enabled: false               # Enable HPA
  minReplicas: 1
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
```

### Server Configuration

```yaml
server:
  listenPort: 8080             # Main server port (default: 8080)
  adminPort: 9090              # Admin server port (default: 9090)
  # HTTP server timeouts
  read_timeout: 15s            # HTTP read timeout (default: 15s) - Go duration format or seconds as number
  write_timeout: 15s           # HTTP write timeout (default: 15s) - Go duration format or seconds as number
  idle_timeout: 60s            # HTTP idle timeout (default: 60s) - Go duration format or seconds as number
  max_body_size: 4GB           # Max body size (default: 4GB) - supports units (GB, MB, KB, B) or raw bytes
  route_cache_size: 1k         # Route cache size (default: 1000) - supports units (k, m) or raw number
```

### Application Configuration

```yaml
config:
  featureFlags:
    enable_caching: true       # Enable response caching
    enable_metrics: true       # Enable metrics collection
  
  backends: {}                 # S3 backends configuration
  buckets: []                  # Virtual bucket routes
```

### Credentials (Stored in Secrets)

```yaml
credentials:
  backend-name:
    access_key_id: "AKIA..."
    secret_access_key: "..."
    assumed_role: ""           # Optional
    sts_session_token: ""      # Optional
```

## Real-World Examples

### Example 1: Multi-Region AWS

```yaml
config:
  backends:
    us-prod:
      endpoint: s3.us-east-1.amazonaws.com
      bucket: prod-us
      prefix: data/
    eu-prod:
      endpoint: s3.eu-west-1.amazonaws.com
      bucket: prod-eu
      prefix: data/
  
  buckets:
    - name: global-bucket
      routes:
        - path: ^us/(?P<rest>.*)
          backend: us-prod
          rewrite:
            - result: $rest
        - path: ^eu/(?P<rest>.*)
          backend: eu-prod
          rewrite:
            - result: $rest

credentials:
  us-prod:
    access_key_id: "AKIA_US_..."
    secret_access_key: "..."
  eu-prod:
    access_key_id: "AKIA_EU_..."
    secret_access_key: "..."
```

### Example 2: Local Dev + Cloud Prod

```yaml
config:
  backends:
    local-dev:
      endpoint: minio:9000
      bucket: dev-bucket
      prefix: stage/
    cloud-prod:
      endpoint: s3.amazonaws.com
      bucket: prod-bucket
      prefix: production/
  
  buckets:
    - name: app-bucket
      routes:
        - path: ^dev/(?P<rest>.*)
          backend: local-dev
          rewrite:
            - result: $rest
        - path: ^prod/(?P<rest>.*)
          backend: cloud-prod
          rewrite:
            - result: $rest

credentials:
  local-dev:
    access_key_id: "minioadmin"
    secret_access_key: "minioadmin"
  cloud-prod:
    access_key_id: "AKIA..."
    secret_access_key: "..."
```

### Example 3: Advanced Routing with Path Rewriting

```yaml
config:
  backends:
    archive:
      endpoint: s3.amazonaws.com
      bucket: archive-bucket
      prefix: archive/
  
  buckets:
    - name: api-bucket
      routes:
        # Transform /api/archive/2024/... to /archive/2024/...
        - path: ^api/archive/(?P<year>[0-9]{4})/(?P<rest>.*)
          backend: archive
          rewrite:
            - result: $year/$rest
        
        # Transform /special/docs/... to /SPECIAL/docs/...
        - path: ^special/(?P<rest>.*)
          backend: archive
          rewrite:
            - pattern: ^special/docs/(.*)
              result: SPECIAL-DOCS/$1
            - pattern: ^special/(.*)
              result: other/$1

credentials:
  archive:
    access_key_id: "AKIA..."
    secret_access_key: "..."
```

## Security Best Practices

### 1. Use External Secret Management

Instead of storing credentials in values.yaml:

**Option A: Using External Secrets Operator**

```yaml
credentials:
  backend1: {}  # Populated by external-secrets operator
```

**Option B: Using Sealed Secrets**

```bash
# Install sealed-secrets controller
helm repo add sealed-secrets https://bitnami-labs.github.io/sealed-secrets

# Create sealed secret
echo -n 'AKIA...' | kubectl create secret generic s3-creds --dry-run=client --from-file=access-key-id=/dev/stdin -o yaml | kubeseal -f - > sealed-s3-creds.yaml

# Reference in values
credentials:
  backend1: {}
```

### 2. RBAC Restrictions

```yaml
# Restrict access to ConfigMaps and Secrets
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: s3-router-reader
rules:
  - apiGroups: [""]
    resources: ["configmaps", "secrets"]
    resourceNames: ["my-s3-router-config", "my-s3-router-credentials"]
    verbs: ["get"]
```

### 3. Network Policies

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: s3-router-netpol
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: s3-router
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: default
  egress:
    - to:
        - namespaceSelector: {}
      ports:
        - protocol: TCP
          port: 443  # HTTPS to S3
```

## Troubleshooting

### Pod won't start

```bash
kubectl describe pod -l app.kubernetes.io/name=s3-router
kubectl logs -l app.kubernetes.io/name=s3-router
```

### Configuration not updating

The chart uses checksum annotations to force pod restart on config changes. If pods don't restart:

```bash
# Force restart
kubectl rollout restart deployment/s3-router
```

### Connectivity issues

```bash
# Test DNS resolution
kubectl run -it --rm debug --image=curlimages/curl -- \
  sh -c "curl -v http://s3-router:80"

# Check service endpoints
kubectl get endpoints s3-router
```

## Uninstall

```bash
helm uninstall s3-router
```

## Chart Version Compatibility

- Kubernetes: 1.19+
- Helm: 3.0+

## Support

For issues or questions:
1. Check the chart README: `chart/README.md`
2. Review example values: `chart/values-example.yaml`
3. Check application logs: `kubectl logs deployment/s3-router`

---

## Deployment Architecture

## Overview

The S3 Router Helm chart provides a production-ready deployment configuration for the S3 Router service. It handles both credentials (stored in Kubernetes Secrets) and routing configuration (rendered in ConfigMaps), with automatic pod restart capabilities.

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        Helm Chart (s3-router)                   │
└─────────────────────────────────────────────────────────────────┘
                                 │
                    ┌────────────┼────────────┐
                    ▼            ▼            ▼
         ┌──────────────┐  ┌───────────┐  ┌──────────┐
         │ values.yaml  │  │  Creds    │  │ Backends │
         │              │  │  Values   │  │ Values   │
         └──────────────┘  └───────────┘  └──────────┘
                    │            │            │
                    └────────────┼────────────┘
                                 │
              ┌──────────────────┼──────────────────┐
              ▼                  ▼                  ▼
         ┌─────────────┐  ┌────────────┐  ┌──────────────┐
         │  ConfigMap  │  │   Secret   │  │ Deployment   │
         │  (config)   │  │(credentials)  │  (pod spec)   │
         └─────────────┘  └────────────┘  └──────────────┘
                                 │
                    ┌────────────┼────────────┐
                    ▼            ▼            ▼
         ┌──────────────┐  ┌───────────┐  ┌──────────┐
         │  Pod Vol.1   │  │Pod Vol. 2 │  │  Env.    │
         │  ConfigMap   │  │ EmptyDirs │  │  Vars    │
         │ (mounted)    │  │(tmp,cache)│  │ (secrets)│
         └──────────────┘  └───────────┘  └──────────┘
                    │
                    ▼
         ┌──────────────────────────┐
         │  s3router Container      │
         │ /etc/s3router/config.yaml│
         │ AWS_ACCESS_KEY_ID (env)  │
         │ AWS_SECRET_ACCESS_KEY    │
         └──────────────────────────┘
                    │
        ┌───────────┼───────────┐
        ▼           ▼           ▼
    ┌────────┐ ┌──────────┐ ┌──────────┐
    │ HTTP   │ │  Admin   │ │ S3 Backend
    │Port 80 │ │Port 9090 │ │Connections
    └────────┘ └──────────┘ └──────────┘
```

## Data Flow

### Configuration Flow

```
User defines values.yaml
        │
        ├─ config.backends (S3 targets)
        ├─ config.buckets (routing rules)
        └─ credentials (AWS keys)
        │
        ▼
Helm renders templates
        │
        ├─ ConfigMap: Full config.yaml with backends + buckets + flags
        ├─ Secret: Base64-encoded credentials
        └─ Deployment: Pod spec with volume mounts + env vars
        │
        ▼
Kubernetes creates resources
        │
        ├─ ConfigMap mounted at /etc/s3router/config.yaml
        ├─ Secret exposed as env vars: AWS_ACCESS_KEY_ID, etc.
        └─ Pod checksum annotations trigger restart on update
        │
        ▼
s3router binary starts
        │
        ├─ Reads /etc/s3router/config.yaml
        ├─ Uses env vars for AWS credentials
        └─ Listens on :8080 (S3 API) and :9090 (admin)
```

## Kubernetes Resource Model

### ConfigMap Structure

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: <release>-s3-router-config
data:
  config.yaml: |
    backends:
      backend1:
        endpoint: s3.amazonaws.com
        bucket: my-bucket
        prefix: data/
        credentials:
          access_key_id: AKIA...
          secret_access_key: ...
    buckets:
      - name: virtual-bucket
        routes:
          - path: ^(.*)
            backend: backend1
            rewrite:
              - result: $1
    features:
      enable_caching: true
```

### Secret Structure

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: <release>-s3-router-credentials
type: Opaque
data:
  access-key-id: QUtJQTEyMzQ1Njc4OTBBQkNERUY=      # base64-encoded
  secret-access-key: d0phbHJYVXRuRkVNSS9LN01ERU5H...  # base64-encoded
  assumed-role: (optional)
  sts-session-token: (optional)
```

### Deployment Specification

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: <release>-s3-router
spec:
  template:
    metadata:
      annotations:
        checksum/config: <hash-of-configmap>   # Triggers pod restart
        checksum/secret: <hash-of-secret>      # Triggers pod restart
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        fsGroup: 65532
      containers:
        - name: s3-router
          image: s3-router:latest
          ports:
            - containerPort: 8080  # S3 API
            - containerPort: 9090  # Admin
          volumeMounts:
            - name: config
              mountPath: /etc/s3router
              readOnly: true
          env:
            - name: AWS_ACCESS_KEY_ID
              valueFrom:
                secretKeyRef:
                  name: <release>-s3-router-credentials
                  key: access-key-id
      volumes:
        - name: config
          configMap:
            name: <release>-s3-router-config
```

## Credential Management

### How Credentials are Handled

1. **User Input**: Credentials specified in `values.yaml` under `credentials` key
2. **Kubernetes Secret**: Credentials stored in Kubernetes Secret with base64 encoding
3. **Container Environment**: Secret values injected as environment variables
4. **Application Usage**: s3router uses AWS SDK which automatically picks up env vars

### Credential Hierarchy

```
values.yaml
  credentials:
    backend1:
      access_key_id: "AKIA..."
      secret_access_key: "..."
         │
         ├─ Backend-specific: Only credentials needed by that backend
         └─ Stored in separate Secret resource
             │
             ├─ Base64-encoded by Kubernetes
             ├─ Mounted as environment variables
             └─ s3router reads via AWS SDK
```

### Security Considerations

1. **Separation of Concerns**: Credentials stored separately from config
2. **Base64 Encoding**: Not encryption, but managed by Kubernetes
3. **Production Recommendations**:
   - Use Sealed Secrets or External Secrets Operator
   - Use RBAC to restrict Secret access
   - Rotate credentials regularly
   - Use IAM roles when possible (avoiding static credentials)

## Configuration Rendering

### Template Processing

```
Helm Template Engine
    │
    ├─ Reads values.yaml
    ├─ Processes {{ }} and {{ - }} expressions
    ├─ Evaluates conditionals ({{- if .Values.xxx }})
    ├─ Loops through maps ({{ range }})
    └─ Calls helpers (_helpers.tpl)
    │
    ▼
Generated Kubernetes YAML
    │
    ├─ ConfigMap with complete config.yaml
    ├─ Secret with encoded credentials
    ├─ Deployment with correct volume mounts
    ├─ Service for external access
    └─ Optional: Ingress, HPA, etc.
    │
    ▼
Applied to Kubernetes cluster
    │
    ├─ Resources created in specified namespace
    ├─ Pods scheduled and started
    └─ Configuration mounted and ready
```

## Pod Lifecycle with Configuration Updates

### Initial Deployment

```
helm install my-release chart
        │
        ▼
Create ConfigMap, Secret, Deployment
        │
        ▼
Pods start, read configuration
        │
        ▼
Ready to serve requests
```

### Configuration Update

```
helm upgrade my-release chart -f new-values.yaml
        │
        ▼
ConfigMap checksum changes
Secret checksum changes
        │
        ▼
Deployment annotations updated
        │
        ▼
Kubernetes detects template change
        │
        ▼
Existing pods terminated
New pods created with new config
        │
        ▼
Zero-downtime update (with replicas > 1)
```

## Health Checks

### Liveness Probe

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 9090
  initialDelaySeconds: 10
  periodSeconds: 10
```

**Purpose**: Restarts pod if service becomes unresponsive

### Readiness Probe

```yaml
readinessProbe:
  httpGet:
    path: /health
    port: 9090
  initialDelaySeconds: 5
  periodSeconds: 5
```

**Purpose**: Removes pod from load balancer if not ready

## Service Exposure

### ClusterIP (Default)
- Internal cluster access only
- DNS: `<release>-s3-router.default.svc.cluster.local`

### NodePort
- Exposes on node IP + fixed port
- External access via `<node-ip>:<node-port>`

### LoadBalancer
- Creates cloud load balancer (AWS/Azure/GCP)
- External IP assigned automatically

### Ingress
- Layer 7 routing (hostname/path-based)
- TLS termination
- Pod restart on config changes handled by checksum

## Autoscaling

### Horizontal Pod Autoscaler (HPA)

```yaml
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
```

**Behavior**:
- Scales out when CPU > 70%
- Scales in when CPU < 70%
- Maintains min/max replica bounds

## File Structure & Separation

```
chart/
├── Chart.yaml              # Chart metadata
├── values.yaml             # Default values (minimal config)
├── values-example.yaml     # Example with production setup
└── templates/
    ├── _helpers.tpl        # Common template functions
    ├── configmap.yaml      # Routing config + backends
    ├── secret.yaml         # Credentials
    ├── deployment.yaml     # Pod spec + security
    ├── service.yaml        # Kubernetes Service
    ├── serviceaccount.yaml # Identity
    ├── hpa.yaml            # Autoscaling (optional)
    ├── ingress.yaml        # External routing (optional)
    └── NOTES.txt           # Post-install instructions
```

## Integration Points

### Input: User Values

```yaml
config:
  backends: {...}      # S3 targets and endpoints
  buckets: [...]       # Routing rules
  featureFlags: {...}  # Feature toggles

credentials: {...}     # AWS credentials

server:                # Server configuration
  listenAddress
  adminAddress

service:               # Kubernetes service
  type, port, etc.

resources:             # Pod resource requests/limits
autoscaling:           # HPA configuration
ingress:               # Ingress configuration
```

### Output: Kubernetes Resources

```
ConfigMap (config.yaml)
├─ Mounted to Pod
└─ Read by s3router binary

Secret (credentials)
├─ Base64-encoded
└─ Injected as env vars

Deployment (pod spec)
├─ Container definition
├─ Volume mounts
├─ Environment variables
├─ Health checks
├─ Security context
└─ Resource constraints

Service
├─ Load balancing
├─ Port mapping
└─ DNS name

HPA (optional)
├─ Scaling policy
└─ Metrics targets

Ingress (optional)
├─ External routing
├─ Hostname/path rules
└─ TLS termination
```

## Deployment Checklist

- [ ] Docker image available in registry
- [ ] Helm installed on local machine
- [ ] Kubernetes cluster accessible
- [ ] Credentials prepared (AWS keys or IAM role)
- [ ] S3 backend endpoints identified
- [ ] Routing rules defined
- [ ] values.yaml configured with credentials
- [ ] Helm chart validated: `helm lint`
- [ ] Manifests previewed: `helm template`
- [ ] Chart installed: `helm install`
- [ ] Pods running: `kubectl get pods`
- [ ] Configuration verified: `kubectl get configmap`
- [ ] Service accessible: `kubectl port-forward`

## Troubleshooting Quick Reference

| Issue | Debug Command |
|-------|---|
| Pod won't start | `kubectl describe pod <pod>` |
| Config not updated | `kubectl rollout restart deploy/<release>` |
| Credentials not working | `kubectl get secret -o yaml` |
| Service not accessible | `kubectl get svc`, `kubectl get endpoints` |
| High memory usage | `kubectl top pod` |
| Pod keeps restarting | `kubectl logs <pod>` |

---

## Complete Guide Reference

This directory contains a production-ready Helm chart for deploying S3 Router to Kubernetes.

## 📚 Documentation Map

### For First-Time Users
1. **[QUICKSTART.md](QUICKSTART.md)** - Start here! 5-minute setup guide with examples
2. **[README.md](README.md)** - Complete reference documentation

### For Understanding Design
- **[ARCHITECTURE.md](ARCHITECTURE.md)** - Deep dive into deployment architecture and data flow

### For Implementation
- **[s3-router/](s3-router/)** - The actual Helm chart
  - `Chart.yaml` - Chart metadata
  - `values.yaml` - Default configuration
  - `values-example.yaml` - Production example
  - `templates/` - Kubernetes resource templates

### For Testing
- **[test-chart.sh](test-chart.sh)** - Automated validation script

## 🚀 Quick Start

### Option 1: Minimal Installation
```bash
helm install my-s3-router s3-router/
```

### Option 2: With Example Configuration
```bash
helm install my-s3-router s3-router/ -f s3-router/values-example.yaml
```

### Option 3: Custom Configuration
```bash
# Create your own values.yaml with credentials and routing
helm install my-s3-router s3-router/ -f my-values.yaml
```

## 📋 Key Features

✅ **Credentials Management**
- Credentials stored in Kubernetes Secrets (not ConfigMaps)
- Automatic base64 encoding/decoding
- Support for AWS access keys, assumed roles, and STS tokens

✅ **Routing Configuration**
- Complete routing rules rendered as YAML in ConfigMaps
- Multi-backend support with regex-based path routing
- Advanced path rewriting and URL transformation

✅ **Production-Ready**
- Automatic pod restart on configuration changes
- Health checks (liveness & readiness probes)
- Resource limits and requests
- Horizontal pod autoscaling (HPA)
- Ingress support for external access

✅ **Security**
- Non-root user (UID 65532)
- Read-only root filesystem
- Dropped all Linux capabilities
- Security context hardening

## 📖 Documentation Index

| Document | Purpose | Audience |
|----------|---------|----------|
| QUICKSTART.md | Quick setup guide with examples | Everyone |
| README.md | Complete reference & API docs | DevOps, Developers |
| ARCHITECTURE.md | Design & data flow diagrams | Architects, Advanced users |
| Chart.yaml | Chart metadata | Helm tools |
| values.yaml | Default configuration | Configuration reference |
| values-example.yaml | Example production setup | Copy as starting point |

## 🏗️ What Gets Created

When you deploy this chart, Kubernetes creates:

```
ConfigMap (routing configuration)
  ├─ backends: S3 endpoint definitions
  ├─ buckets: Virtual bucket routing rules  
  └─ features: Feature toggles

Secret (credentials)
  ├─ access_key_id (base64-encoded)
  ├─ secret_access_key (base64-encoded)
  ├─ assumed_role (optional)
  └─ sts_session_token (optional)

Deployment (pod specification)
  ├─ Mounts ConfigMap as /etc/s3router/config.yaml
  ├─ Injects Secret as environment variables
  ├─ Checksums for automatic pod restart
  └─ Security context hardening

Service (load balancer)
  ├─ Port 8080 (S3 API)
  └─ Port 9090 (Admin API)

ServiceAccount (pod identity)
  └─ Optional RBAC configuration

HPA (optional, for autoscaling)
  └─ Scales based on CPU utilization

Ingress (optional, for external access)
  └─ TLS termination & hostname routing
```

## 🔧 Common Tasks

### Deploy with custom configuration
```bash
helm install my-release s3-router/ \
  -f my-config.yaml \
  --namespace my-namespace
```

### Update configuration
```bash
helm upgrade my-release s3-router/ \
  -f updated-config.yaml
```

### View deployed configuration
```bash
kubectl get configmap -l app.kubernetes.io/name=s3-router -o yaml
```

### Check pod status
```bash
kubectl get pods -l app.kubernetes.io/name=s3-router
```

### View logs
```bash
kubectl logs -f deployment/my-release-s3-router
```

### Access S3 API
```bash
kubectl port-forward svc/my-release-s3-router 8080:80
aws s3 ls --endpoint-url http://localhost:8080 --no-sign-request
```

## 📝 Configuration Structure

### values.yaml Organization

```yaml
image:                  # Docker image configuration
  repository
  tag
  pullPolicy

replicaCount:          # Number of pods

service:               # Kubernetes Service
  type
  port
  adminPort

ingress:               # External access (optional)
  enabled
  hosts
  tls

resources:             # Pod resource limits
  limits
  requests

autoscaling:           # HPA configuration (optional)
  enabled
  minReplicas
  maxReplicas

config:                # S3 Router configuration
  backends:            # S3 targets (endpoints, buckets)
  buckets:             # Virtual bucket routing rules
  featureFlags:        # Feature toggles

credentials:           # AWS credentials (stored in Secret)
  <backend-name>:
    access_key_id
    secret_access_key
    assumed_role
    sts_session_token

server:                # Server configuration
  listenAddress
  adminAddress
```

## 🔐 Security Best Practices

1. **Use Secret Management Tool**
   - Sealed Secrets
   - External Secrets Operator
   - HashiCorp Vault

2. **RBAC Restrictions**
   - Limit access to ConfigMaps and Secrets
   - Use Pod Security Policies

3. **Network Policies**
   - Restrict pod-to-pod communication
   - Allow only necessary S3 egress

4. **Regular Updates**
   - Keep Docker image current
   - Rotate credentials regularly
   - Use IAM roles instead of static keys

## ✅ Testing

Run the validation script:
```bash
./test-chart.sh
```

This validates:
- Chart linting
- Template rendering
- Kubernetes manifest validity
- Resource creation
- Configuration correctness

## 📊 Examples

### Simple Single Backend
See [QUICKSTART.md](QUICKSTART.md#example-1-simple-single-backend)

### Multi-Region AWS Setup
See [QUICKSTART.md](QUICKSTART.md#example-2-multi-region-setup)

### Advanced Routing & Rewriting
See [QUICKSTART.md](QUICKSTART.md#example-3-advanced-routing-with-path-rewriting)

## 🐛 Troubleshooting

### Pod won't start
```bash
kubectl describe pod <pod-name>
kubectl logs <pod-name>
```

### Configuration not updating
```bash
# Check ConfigMap
kubectl get configmap -o yaml

# Force pod restart
kubectl rollout restart deployment/<release>-s3-router
```

### Service not accessible
```bash
kubectl get svc
kubectl get endpoints
kubectl port-forward svc/<release>-s3-router 8080:80
```

For more details, see the troubleshooting section in [README.md](README.md#troubleshooting)

## 🔍 OpenTelemetry Configuration

This guide explains how to configure OpenTelemetry tracing and metrics collection for the s3-router Helm chart.

### Quick Start

#### Enable OTel with Default Settings

```bash
helm install my-s3-router ./chart \
  --set opentelemetry.enabled=true \
  --set opentelemetry.exporter.endpoint="http://jaeger:4318"
```

#### Disable OTel

```bash
helm install my-s3-router ./chart \
  --set opentelemetry.enabled=false
```

#### Use Production Settings (10% Sampling)

```bash
helm install my-s3-router ./chart \
  --set opentelemetry.enabled=true \
  --set opentelemetry.exporter.endpoint="http://otel-collector:4318" \
  --set opentelemetry.traces.sampler="traceidratio" \
  --set opentelemetry.traces.samplerArg="0.1"
```

### Configuration Reference

#### Root Configuration

```yaml
opentelemetry:
  enabled: true                    # Enable/disable OTel instrumentation
  exporter: {...}                  # OTLP exporter configuration
  traces: {...}                    # Trace-specific settings
  metrics: {...}                   # Metric-specific settings
  service: {...}                   # Service identification
  sdk: {...}                       # SDK settings
```

#### Exporter Configuration

```yaml
opentelemetry:
  exporter:
    endpoint: "http://localhost:4318"   # OTLP HTTP endpoint
    timeout: "10s"                      # Exporter request timeout
```

**Options:**
- `endpoint`: OTLP HTTP endpoint (default: `http://localhost:4318`)
  - Jaeger: `http://jaeger:4318`
  - OTel Collector: `http://otel-collector:4318`
  - Datadog: `http://localhost:4318` (after setup)
  - New Relic: `http://localhost:4318` (after setup)

- `timeout`: Request timeout in Go duration format (default: `10s`)

#### Trace Configuration

```yaml
opentelemetry:
  traces:
    exporter: "otlp"           # Only option currently
    sampler: "always_on"       # Sampler type
    samplerArg: "1.0"          # Sampler argument
```

**Sampler Options:**
- `always_on`: Sample all traces (default for development)
- `always_off`: Don't sample any traces (useful for testing)
- `traceidratio`: Sample a percentage of traces

**Sampler Argument:**
- `always_on`: Ignored
- `always_off`: Ignored
- `traceidratio`: Sampling ratio (0.0 - 1.0)
  - `0.1` = 10% sampling
  - `0.01` = 1% sampling
  - `1.0` = 100% sampling

#### Metrics Configuration

```yaml
opentelemetry:
  metrics:
    exporter: "otlp"           # Only option currently
```

#### Service Configuration

```yaml
opentelemetry:
  service:
    name: "s3-router"          # Service name for span tags
    version: "0.1.0"           # Service version
```

#### SDK Configuration

```yaml
opentelemetry:
  sdk:
    disabled: false            # Disable entire SDK (for testing)
```

### Integration Examples

#### Jaeger (Development)

1. **Deploy Jaeger:**
```bash
docker run -d \
  -p 16686:16686 \
  -p 4318:4318 \
  jaegertracing/all-in-one:latest
```

2. **Deploy s3-router:**
```bash
helm install my-s3-router ./chart \
  --set opentelemetry.enabled=true \
  --set opentelemetry.exporter.endpoint="http://localhost:4318"
```

3. **View Traces:**
```
Open http://localhost:16686 → Select "s3-router" service
```

#### Kubernetes with Jaeger

```bash
# 1. Deploy Jaeger in Kubernetes
kubectl create namespace jaeger
kubectl apply -f examples/otel-deployment.yaml

# 2. Deploy s3-router pointing to Jaeger
helm install my-s3-router ./chart \
  -f examples/otel-jaeger-values.yaml

# 3. Access Jaeger UI
kubectl port-forward -n jaeger svc/jaeger 16686:16686
# Open http://localhost:16686
```

#### OpenTelemetry Collector

1. **Deploy OTel Collector:**
```bash
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm install opentelemetry-collector open-telemetry/opentelemetry-collector \
  --set mode=daemonset
```

2. **Deploy s3-router:**
```bash
helm install my-s3-router ./chart \
  --set opentelemetry.enabled=true \
  --set opentelemetry.exporter.endpoint="http://otel-collector:4318"
```

#### Production with Sampling

```bash
helm install my-s3-router ./chart \
  -f examples/otel-production-values.yaml
```

Key features:
- 10% trace sampling (via `traceidratio`)
- Configured for opentelemetry-collector
- Production resource limits
- Autoscaling enabled

### Deployment Examples

#### Example 1: Local Development with Jaeger

```bash
cd chart/examples
helm install my-s3-router .. -f opentelemetry-jaeger-values.yaml
```

#### Example 2: Production Setup

```bash
cd chart/examples
helm install my-s3-router .. -f opentelemetry-production-values.yaml
```

#### Example 3: Custom Endpoint

```bash
helm install my-s3-router ./chart \
  --set opentelemetry.enabled=true \
  --set opentelemetry.exporter.endpoint="https://otel.example.com:4318"
```

#### Example 4: High-Volume with Sampling

```bash
helm install my-s3-router ./chart \
  --set opentelemetry.enabled=true \
  --set opentelemetry.exporter.endpoint="http://otel-collector:4318" \
  --set opentelemetry.traces.sampler="traceidratio" \
  --set opentelemetry.traces.samplerArg="0.01"  # 1% sampling
```

### OpenTelemetry Troubleshooting

#### Traces Not Appearing

1. **Verify OTel is enabled:**
```bash
kubectl describe pod -l app.kubernetes.io/name=s3-router | grep OTEL
```

2. **Check endpoint connectivity:**
```bash
kubectl exec -it <pod-name> -- \
  curl -v http://jaeger:4318/v1/traces
```

3. **Check pod logs:**
```bash
kubectl logs -f deployment/my-s3-router-s3-router
```

#### High Memory Usage

1. **Reduce sampling rate:**
```bash
helm upgrade my-s3-router ./chart \
  --set opentelemetry.traces.sampler="traceidratio" \
  --set opentelemetry.traces.samplerArg="0.01"
```

2. **Verify collector is healthy:**
```bash
kubectl get pods -l app=otel-collector
kubectl logs -f pod/otel-collector-0
```

#### Endpoint Connection Error

1. **Verify endpoint is reachable:**
```bash
kubectl run debug --image=curlimages/curl -it -- sh
curl -v http://jaeger:4318/v1/traces
```

2. **Check DNS resolution:**
```bash
kubectl exec -it <pod-name> -- nslookup jaeger.jaeger.svc.cluster.local
```

3. **Verify network policies:**
```bash
kubectl get networkpolicy
```

#### Environment Variables Not Set

1. **Verify OTel is enabled in values:**
```bash
helm get values my-s3-router | grep -A 10 "^opentelemetry:"
```

2. **Check rendered manifest:**
```bash
helm template my-s3-router ./chart | grep OTEL_
```

### Performance Tuning

#### Development (Sample Everything)

```yaml
opentelemetry:
  traces:
    sampler: "always_on"
    samplerArg: "1.0"
```

#### Testing (Sample Nothing)

```yaml
opentelemetry:
  traces:
    sampler: "always_off"
    samplerArg: "0"
```

#### Production (10% Sampling)

```yaml
opentelemetry:
  traces:
    sampler: "traceidratio"
    samplerArg: "0.1"
```

#### High-Volume (1% Sampling)

```yaml
opentelemetry:
  traces:
    sampler: "traceidratio"
    samplerArg: "0.01"
```

### Environment Variables Generated

When OTel is enabled, the following environment variables are set in the pod:

```
OTEL_EXPORTER_OTLP_ENDPOINT     → opentelemetry.exporter.endpoint
OTEL_EXPORTER_OTLP_TIMEOUT      → opentelemetry.exporter.timeout
OTEL_TRACES_EXPORTER            → opentelemetry.traces.exporter
OTEL_TRACES_SAMPLER             → opentelemetry.traces.sampler
OTEL_TRACES_SAMPLER_ARG         → opentelemetry.traces.samplerArg
OTEL_METRICS_EXPORTER           → opentelemetry.metrics.exporter
OTEL_SERVICE_NAME               → opentelemetry.service.name
OTEL_SERVICE_VERSION            → opentelemetry.service.version
OTEL_SDK_DISABLED               → opentelemetry.sdk.disabled
```

### OTel References

- [OpenTelemetry Documentation](https://opentelemetry.io/docs/)
- [OTLP Protocol](https://github.com/open-telemetry/opentelemetry-proto)
- [Jaeger Documentation](https://www.jaegertracing.io/docs/)
- [OTel Collector Documentation](https://opentelemetry.io/docs/collector/)

## 📞 Support

1. Review the README.md for detailed documentation
2. Check ARCHITECTURE.md for design explanations
3. Review example configurations in values-example.yaml
4. Run test-chart.sh to validate your setup
5. Check application logs: `kubectl logs deployment/...`

## 📄 File Manifest

```
chart/
├── INDEX.md                    # This file
├── README.md                   # Complete reference (8.4 KB)
├── QUICKSTART.md               # Quick start guide (9.0 KB)
├── ARCHITECTURE.md             # Design & architecture (11.6 KB)
├── test-chart.sh               # Validation script
└── s3-router/
    ├── Chart.yaml              # Chart metadata
    ├── values.yaml             # Default values
    ├── values-example.yaml     # Example production config
    ├── .helmignore             # Files to exclude
    └── templates/
        ├── _helpers.tpl        # Template helpers
        ├── configmap.yaml      # Routing config
        ├── secret.yaml         # Credentials
        ├── deployment.yaml     # Pod specification
        ├── service.yaml        # Kubernetes Service
        ├── serviceaccount.yaml # Pod identity
        ├── hpa.yaml            # Autoscaling
        ├── ingress.yaml        # External routing
        └── NOTES.txt           # Post-install notes
```

## 🎯 Next Steps

1. Read [QUICKSTART.md](QUICKSTART.md) for immediate setup
2. Customize [s3-router/values.yaml](s3-router/values.yaml) or create a new file
3. Run `./test-chart.sh` to validate
4. Deploy with `helm install my-s3-router s3-router/ -f values.yaml`
5. Verify with `kubectl get all -l app.kubernetes.io/name=s3-router`

---

**Chart Version**: 0.1.0  
**Kubernetes**: 1.19+  
**Helm**: 3.0+