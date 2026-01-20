"""Pytest configuration and fixtures for helm integration tests."""

from __future__ import annotations

import logging
import os
import socket
import subprocess
import tempfile
import time
from collections.abc import Iterator as IteratorABC
from contextlib import contextmanager
from pathlib import Path
from typing import TYPE_CHECKING, Any, Callable, Iterator

import boto3
import pytest
import urllib3.util.connection
import yaml
from kubernetes import client, config  # type: ignore[import]
from kubernetes.client import ApiClient, ApiException  # type: ignore[import]
from kubernetes.stream import portforward  # type: ignore[import]

if TYPE_CHECKING:
    from kubernetes.client import AppsV1Api, CoreV1Api, V1Pod
    from types_boto3_s3 import S3Client

# Test constants
TEST_NAMESPACE = "s3-router-test"
RELEASE_NAME = "s3-router-test"
HELM_CHART_PATH = "../../chart"
TEST_VALUES_FILE = "test-values.yaml"
KIND_CLUSTER_NAME = "s3-router-test"

# Environment variables for CA certificates
EXTRA_CA_CERTS_ENV = "EXTRA_CA_CERTS"  # Path to PEM file or directory
REQUESTS_CA_BUNDLE_ENV = "REQUESTS_CA_BUNDLE"  # For requests library
CURL_CA_BUNDLE_ENV = "CURL_CA_BUNDLE"  # For curl/helm

logger = logging.getLogger(__name__)

# Configure logging
logging.basicConfig(level=logging.INFO, format="%(asctime)s - %(name)s - %(levelname)s - %(message)s")


@contextmanager
def kubernetes_portforward(
    v1_api: CoreV1Api,
    namespace: str,
) -> IteratorABC[None]:
    """Context manager that provides a socket factory for port-forwarding to a Kubernetes service.

    This uses the Kubernetes Python client's portforward functionality to create
    a direct socket connection to a pod backing the specified service.

    Args:
        v1_api: Kubernetes CoreV1 API client
        namespace: Kubernetes namespace

    Yields:
        None (sets up socket factory via urllib3 monkey-patching)
    """

    def create_socket(service_name: str, port: int) -> socket.socket:
        # Find a pod backing the service
        service = v1_api.read_namespaced_service(service_name, namespace)

        # Find pods using service selector
        label_selector = ",".join(f"{k}={v}" for k, v in service.spec.selector.items())
        pods = v1_api.list_namespaced_pod(namespace, label_selector=label_selector)

        if not pods.items:
            raise RuntimeError(f"No pods found for service {service_name}")

        logger.info(f"Port-forwarding to service {service_name} port {port}")

        pf = portforward(
            v1_api.connect_get_namespaced_pod_portforward,
            pods.items[0].metadata.name,
            namespace,
            ports=str(port),
        )
        return pf.socket(port)

    # Monkey-patch urllib3's create_connection to use port-forward for moto
    original_create_connection = urllib3.util.connection.create_connection

    def patched_create_connection(address: tuple[str, int], *args: Any, **kwargs: Any) -> socket.socket:
        host, port = address
        # Intercept connections to our endpoint
        if host.endswith(".k8s"):
            return create_socket(host.removesuffix(".k8s"), port)
        return original_create_connection(address, *args, **kwargs)

    urllib3.util.connection.create_connection = patched_create_connection  # type: ignore[assignment]

    try:
        yield
    finally:
        urllib3.util.connection.create_connection = original_create_connection  # type: ignore[method-assign]


class SetupError(Exception):
    pass


def get_ca_bundle_path() -> str | None:
    """Get path to CA certificate bundle from environment variables."""
    ca_path = os.environ.get(EXTRA_CA_CERTS_ENV)
    if ca_path:
        path = Path(ca_path)
        if path.exists():
            logger.info(f"✓ Using CA certificates from: {ca_path}")
            return ca_path
        else:
            logger.warning(f"CA certificate path does not exist: {ca_path}")
    return None


def setup_ca_environment() -> dict[str, str]:
    """Configure environment variables for CA certificates."""
    ca_path = get_ca_bundle_path()
    if not ca_path:
        logger.debug("No extra CA certificates configured")
        return {}

    # Create environment dict with CA settings
    env_overrides = {
        REQUESTS_CA_BUNDLE_ENV: ca_path,  # For Python requests library
        CURL_CA_BUNDLE_ENV: ca_path,  # For curl and helm
    }

    logger.info(f"Configuring CA certificate environment: {ca_path}")
    return env_overrides


def run_command(cmd: list[str], timeout: int = 30, use_ca_env: bool = True) -> tuple[int, str, str]:
    """Run a shell command and return result."""
    try:
        env = os.environ.copy()
        if use_ca_env:
            ca_env = setup_ca_environment()
            env.update(ca_env)

        result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=env)
        return result.returncode, result.stdout, result.stderr
    except subprocess.TimeoutExpired:
        raise SetupError(f"Command timed out: {' '.join(cmd)}")
    except Exception as e:
        raise SetupError(f"Command failed: {' '.join(cmd)}: {e}")


def get_kind_config() -> dict[str, Any]:
    """Generate kind cluster config, optionally with CA certificate support."""
    ca_path = get_ca_bundle_path()

    config_dict: dict[str, Any] = {
        "kind": "Cluster",
        "apiVersion": "kind.x-k8s.io/v1alpha4",
        "name": KIND_CLUSTER_NAME,
        "nodes": [{"role": "control-plane"}],
    }

    # Add CA certificate mounting if provided
    if ca_path:
        logger.info(f"Adding CA certificate to kind cluster config: {ca_path}")
        ca_file = Path(ca_path)

        if ca_file.is_file():
            # Single certificate file - mount it to the control plane
            config_dict["nodes"][0]["extraMounts"] = [
                {"hostPath": ca_path, "containerPath": "/etc/ssl/certs/extra-ca.pem", "readOnly": True}
            ]
        elif ca_file.is_dir():
            # Certificate directory - mount it
            config_dict["nodes"][0]["extraMounts"] = [{"hostPath": ca_path, "containerPath": "/etc/ssl/certs/custom-ca", "readOnly": True}]

    return config_dict


def wait_for_condition(condition_func: Callable[[], bool], timeout: int = 300, interval: int = 5) -> bool:
    """Wait for a condition to become true."""
    start_time = time.time()
    while time.time() - start_time < timeout:
        try:
            if condition_func():
                return True
        except Exception as e:
            logger.debug(f"Condition check failed: {e}")
        time.sleep(interval)
    raise SetupError(f"Condition not met within {timeout} seconds")


def get_pod_details(v1_api: CoreV1Api, pod_name: str, namespace: str) -> None:
    """Fetch and log detailed pod information for debugging."""
    try:
        pod = v1_api.read_namespaced_pod(name=pod_name, namespace=namespace)

        logger.error("=== POD DETAILS ===")
        logger.error(f"Name: {pod.metadata.name}")
        logger.error(f"Namespace: {pod.metadata.namespace}")
        logger.error(f"Phase: {pod.status.phase}")
        logger.error(f"Host IP: {pod.status.host_ip}")
        logger.error(f"Pod IP: {pod.status.pod_ip}")

        if pod.status.conditions:
            logger.error("Conditions:")
            for condition in pod.status.conditions:
                logger.error(f"  - {condition.type}: {condition.status} ({condition.reason})")
                if condition.message:
                    logger.error(f"    Message: {condition.message}")

        if pod.status.container_statuses:
            logger.error("Container Status:")
            for container_status in pod.status.container_statuses:
                logger.error(f"  - {container_status.name}:")
                logger.error(f"    Image: {container_status.image}")
                logger.error(f"    Ready: {container_status.ready}")
                logger.error(f"    Restart Count: {container_status.restart_count}")

                if container_status.state.waiting:
                    logger.error("    State: Waiting")
                    logger.error(f"    Reason: {container_status.state.waiting.reason}")
                    if container_status.state.waiting.message:
                        logger.error(f"    Message: {container_status.state.waiting.message}")
                elif container_status.state.running:
                    logger.error(f"    State: Running (started at {container_status.state.running.started_at})")
                elif container_status.state.terminated:
                    logger.error("    State: Terminated")
                    logger.error(f"    Exit Code: {container_status.state.terminated.exit_code}")
                    logger.error(f"    Reason: {container_status.state.terminated.reason}")
                    if container_status.state.terminated.message:
                        logger.error(f"    Message: {container_status.state.terminated.message}")

                if container_status.last_state and (container_status.last_state.terminated or container_status.last_state.waiting):
                    logger.error(f"    Last State: {container_status.last_state}")

                # Fetch container logs
                try:
                    logs = v1_api.read_namespaced_pod_log(
                        name=pod_name, namespace=namespace, container=container_status.name, tail_lines=20
                    )
                    if logs:
                        logger.error("    Last 20 lines of output:")
                        for line in logs.split("\n"):
                            if line.strip():
                                logger.error(f"      {line}")
                except Exception as log_error:
                    logger.error(f"    Could not fetch logs: {log_error}")

        if pod.status.init_container_statuses:
            logger.error("Init Container Status:")
            for init_container_status in pod.status.init_container_statuses:
                logger.error(f"  - {init_container_status.name}: {init_container_status.state}")

                # Fetch init container logs
                try:
                    logs = v1_api.read_namespaced_pod_log(
                        name=pod_name, namespace=namespace, container=init_container_status.name, tail_lines=20
                    )
                    if logs:
                        logger.error("    Last 20 lines of output:")
                        for line in logs.split("\n"):
                            if line.strip():
                                logger.error(f"      {line}")
                except Exception as log_error:
                    logger.error(f"    Could not fetch logs: {log_error}")

        logger.error("=== END POD DETAILS ===")
    except Exception as e:
        logger.error(f"Failed to get pod details: {e}")


@pytest.fixture(scope="session")
def no_cleanup_cluster() -> bool:
    match os.environ.get("NO_CLEANUP_CLUSTER", "false"):
        case "0" | "false":
            return False
        case _:
            return True


@pytest.fixture(scope="session")
def build_docker_image(no_cleanup_cluster: bool) -> Iterator[str]:
    """Build s3-router docker image for integration tests."""
    logger.info("Building s3-router docker image for integration tests...")

    # Get the project root directory (parent of tests directory)
    script_dir = Path(__file__).parent
    project_root = (script_dir / "../../").resolve()
    dockerfile_path = project_root / "Dockerfile"

    if not dockerfile_path.exists():
        raise SetupError(f"Dockerfile not found at {dockerfile_path}")

    image = "s3-router:integration-test"

    # Check if image already exists
    returncode, stdout, _ = run_command(["docker", "image", "inspect", image], use_ca_env=False)
    if returncode == 0:
        logger.info(f"✓ Docker image '{image}' already exists")
    else:
        logger.info(f"Building docker image from {dockerfile_path}...")
        returncode, stdout, stderr = run_command(
            ["docker", "build", "-t", image, "-f", str(dockerfile_path), "--target", "debug", str(project_root)],
            timeout=600,
            use_ca_env=False,
        )

        if returncode != 0:
            raise SetupError(f"Failed to build docker image: {stderr}")
        logger.info(f"✓ Docker image '{image}' built successfully")

    yield image

    if no_cleanup_cluster:
        return

    # Cleanup: remove the docker image
    logger.info(f"Cleaning up docker image '{image}'...")
    returncode, _, stderr = run_command(["docker", "rmi", "-f", image], use_ca_env=False)
    if returncode != 0:
        logger.warning(f"Failed to remove docker image: {stderr}")
    else:
        logger.info(f"✓ Docker image '{image}' removed")


@pytest.fixture(scope="session")
def setup_kind_cluster(no_cleanup_cluster: bool, build_docker_image: str) -> Iterator[None]:
    """Create kind cluster once per test session."""
    logger.info(f"Setting up kind cluster: {KIND_CLUSTER_NAME}")

    # Check if kind is installed
    returncode, _, stderr = run_command(["kind", "version"])
    if returncode != 0:
        raise SetupError(f"kind CLI not found: {stderr}")

    # Check if cluster already exists
    returncode, stdout, _ = run_command(["kind", "get", "clusters"])
    if KIND_CLUSTER_NAME in stdout:
        logger.info(f"✓ Kind cluster '{KIND_CLUSTER_NAME}' already exists")
    else:
        logger.info(f"Creating kind cluster '{KIND_CLUSTER_NAME}'...")

        # Generate kind config with CA certificate support if needed
        kind_config = get_kind_config()

        # Write kind config to temporary file
        with tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False) as f:
            yaml.dump(kind_config, f)
            kind_config_file = f.name

        logger.info(f"Kind config written to {kind_config_file}")

        try:
            # Create cluster using config file
            returncode, _, stderr = run_command(["kind", "create", "cluster", "--config", kind_config_file, "--wait", "5m"], timeout=600)
            if returncode != 0:
                raise SetupError(f"Failed to create kind cluster: {stderr}")
            logger.info("✓ Kind cluster created")
        finally:
            # Cleanup kind config file
            try:
                os.unlink(kind_config_file)
            except Exception as e:
                logger.warning(f"Failed to cleanup kind config: {e}")

    # Load s3-router image into kind
    logger.info("Loading s3-router image into kind cluster...")
    returncode, _, stderr = run_command(["kind", "load", "docker-image", build_docker_image, "--name", KIND_CLUSTER_NAME])
    if returncode != 0:
        raise SetupError(f"Failed to load image into kind cluster: {stderr}")
    logger.info("✓ s3-router image loaded into kind cluster")

    yield

    if no_cleanup_cluster:
        return

    # Cleanup: delete kind cluster
    logger.info(f"Deleting kind cluster '{KIND_CLUSTER_NAME}'...")
    returncode, _, stderr = run_command(["kind", "delete", "cluster", "--name", KIND_CLUSTER_NAME])
    if returncode != 0:
        logger.warning(f"Failed to delete kind cluster: {stderr}")
    else:
        logger.info("✓ Kind cluster deleted")


@pytest.fixture(scope="session")
def kube_config_context(setup_kind_cluster: None) -> Iterator[str]:
    """Load Kubernetes configuration from kind cluster."""
    logger.info("Loading Kubernetes configuration from kind...")

    # Get kubeconfig for kind cluster
    returncode, kubeconfig_content, stderr = run_command(["kind", "get", "kubeconfig", "--name", KIND_CLUSTER_NAME], timeout=30)

    if returncode != 0:
        raise SetupError(f"Failed to get kubeconfig from kind: {stderr}")

    # Write kubeconfig to temporary file
    with tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False) as f:
        f.write(kubeconfig_content)
        kubeconfig_file = f.name

    logger.info(f"Kubeconfig written to {kubeconfig_file}")

    # Load the configuration
    try:
        config.load_kube_config(config_file=kubeconfig_file)
        logger.info("✓ Kubernetes configuration loaded from kind")
    except Exception as e:
        raise SetupError(f"Failed to load kubeconfig: {e}")

    yield kubeconfig_file

    # Cleanup
    try:
        os.unlink(kubeconfig_file)
    except Exception as e:
        logger.warning(f"Failed to cleanup kubeconfig: {e}")


@pytest.fixture(scope="session")
def api_client(kube_config_context: str) -> ApiClient:
    """Get Kubernetes API client."""
    logger.info("Creating Kubernetes API client...")
    api_client_instance = client.ApiClient()
    logger.info("✓ API client created")
    return api_client_instance


@pytest.fixture(scope="session")
def v1_api(api_client: ApiClient) -> CoreV1Api:
    """Get Kubernetes CoreV1 API."""
    logger.info("Creating CoreV1 API...")
    v1 = client.CoreV1Api(api_client)
    logger.info("✓ CoreV1 API created")
    return v1


@pytest.fixture(scope="session")
def apps_v1_api(api_client: ApiClient) -> AppsV1Api:
    """Get Kubernetes AppsV1 API."""
    logger.info("Creating AppsV1 API...")
    apps_v1 = client.AppsV1Api(api_client)
    logger.info("✓ AppsV1 API created")
    return apps_v1


@pytest.fixture(scope="session")
def test_namespace(v1_api: CoreV1Api, no_cleanup_cluster: bool) -> Iterator[str]:
    """Create and cleanup test namespace."""
    logger.info(f"Creating namespace {TEST_NAMESPACE}...")

    # Check if namespace exists
    try:
        v1_api.read_namespace(TEST_NAMESPACE)
        logger.info(f"✓ Namespace {TEST_NAMESPACE} already exists")
    except ApiException as e:
        if e.status == 404:
            # Create namespace
            ns = client.V1Namespace(metadata=client.V1ObjectMeta(name=TEST_NAMESPACE))
            v1_api.create_namespace(ns)
            logger.info(f"✓ Namespace {TEST_NAMESPACE} created")
        else:
            raise

    yield TEST_NAMESPACE

    if no_cleanup_cluster:
        return

    # Cleanup: delete namespace
    logger.info(f"Cleaning up namespace {TEST_NAMESPACE}...")
    try:
        v1_api.delete_namespace(TEST_NAMESPACE, body=client.V1DeleteOptions(grace_period_seconds=0))
        logger.info(f"✓ Namespace {TEST_NAMESPACE} deleted")
    except ApiException as e:
        if e.status != 404:
            logger.warning(f"Failed to delete namespace: {e}")


@pytest.fixture(scope="session")
def moto_pod(test_namespace: str, v1_api: CoreV1Api, no_cleanup_cluster: bool) -> Iterator[V1Pod]:
    """Deploy moto S3 mock as a Kubernetes pod with event monitoring."""
    logger.info("Deploying moto S3 mock pod to Kubernetes...")

    # Create moto pod
    moto_pod = client.V1Pod(
        api_version="v1",
        kind="Pod",
        metadata=client.V1ObjectMeta(name="moto", namespace=test_namespace, labels={"app": "moto"}),
        spec=client.V1PodSpec(
            containers=[
                client.V1Container(
                    name="moto",
                    image="motoserver/moto",
                    ports=[client.V1ContainerPort(container_port=5000)],
                    resources=client.V1ResourceRequirements(
                        requests={"cpu": "100m", "memory": "128Mi"}, limits={"cpu": "500m", "memory": "512Mi"}
                    ),
                    env=[
                        client.V1EnvVar(
                            name="MOTO_PORT",
                            value="5000",
                        ),
                        client.V1EnvVar(
                            name="S3_IGNORE_SUBDOMAIN_BUCKETNAME",
                            value="true",
                        ),
                    ],
                    readiness_probe=client.V1Probe(
                        http_get=client.V1HTTPGetAction(path="/moto-api/data.json", port=5000),
                        initial_delay_seconds=1,
                        period_seconds=5,
                        timeout_seconds=2,
                        success_threshold=1,
                        failure_threshold=3,
                    ),
                    liveness_probe=client.V1Probe(
                        http_get=client.V1HTTPGetAction(path="/moto-api/data.json", port=5000),
                        initial_delay_seconds=1,
                        period_seconds=10,
                        timeout_seconds=2,
                        success_threshold=1,
                        failure_threshold=3,
                    ),
                )
            ],
            restart_policy="Never",
        ),
    )

    try:
        v1_api.create_namespaced_pod(test_namespace, moto_pod)
        logger.info("✓ Moto pod created")
    except ApiException as e:
        if e.status != 409:  # 409 = already exists
            raise
        logger.info("✓ Moto pod already exists")

    yield moto_pod

    if no_cleanup_cluster:
        return

    v1_api.delete_namespaced_pod(name="moto", namespace=test_namespace, body=client.V1DeleteOptions(grace_period_seconds=5))


@pytest.fixture(scope="session")
def _moto_service_inner(test_namespace: str, v1_api: CoreV1Api, moto_pod: V1Pod, no_cleanup_cluster: bool) -> Iterator[None]:
    """Deploy moto S3 mock as a Kubernetes pod with event monitoring."""
    logger.info("Deploying moto S3 mock pod to Kubernetes...")

    # Create moto service
    moto_service_obj = client.V1Service(
        api_version="v1",
        kind="Service",
        metadata=client.V1ObjectMeta(name="moto", namespace=test_namespace),
        spec=client.V1ServiceSpec(
            selector=moto_pod.metadata.labels,  # {"app": "moto"},
            ports=[
                client.V1ServicePort(
                    port=5000,
                    target_port=moto_pod.spec.containers[0].ports[0].container_port,
                ),
            ],
            type="ClusterIP",
        ),
    )

    try:
        v1_api.create_namespaced_service(test_namespace, moto_service_obj)
        logger.info("✓ Moto service created")
    except ApiException as e:
        if e.status != 409:  # 409 = already exists
            raise
        logger.info("✓ Moto service already exists")

    yield

    if no_cleanup_cluster:
        return

    # Cleanup: delete pod and service
    logger.info("Cleaning up moto pod...")
    try:
        v1_api.delete_namespaced_service(name="moto", namespace=test_namespace, body=client.V1DeleteOptions(grace_period_seconds=0))
        logger.info("✓ Moto pod and service deleted")
    except ApiException as e:
        if e.status != 404:
            logger.warning(f"Failed to delete moto resources: {e}")


@pytest.fixture(scope="session")
def moto_service(test_namespace: str, v1_api: CoreV1Api, _moto_service_inner: None) -> str:
    # Watch pod events for failures and readiness
    logger.info("Waiting for moto pod to be ready...")
    client.EventsV1Api(v1_api.api_client)
    start_time = time.time()
    timeout = 120

    try:
        while time.time() - start_time < timeout:
            time.sleep(1)
            try:
                # Check pod status
                pod = v1_api.read_namespaced_pod(name="moto", namespace=test_namespace)
            except ApiException as e:
                if e.status != 404:
                    logger.warning(f"Error checking pod status: {e}")
                continue

            match pod.status.phase:
                case "Running":
                    for container_status in pod.status.container_statuses:
                        if container_status.ready:
                            break
                    else:
                        continue
                    break
                case "Failed":
                    raise SetupError(f"Moto pod failed to start with phase: {pod.status.phase}")
                case _:
                    for container_status in pod.status.container_statuses:
                        if container_status.state.waiting:
                            waiting_reason = container_status.state.waiting.reason
                            # Check for error conditions
                            if waiting_reason in ["ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff", "Error"]:
                                raise SetupError(
                                    f"Moto pod failed to start: {waiting_reason}. Message: {container_status.state.waiting.message}"
                                )
        else:
            # Timeout reached - show pod details before bailing
            logger.error(f"Moto pod did not become ready within {timeout} seconds")
            raise SetupError(f"Moto pod did not become ready within {timeout} seconds")
    except Exception as e:
        get_pod_details(v1_api, "moto", test_namespace)
        logger.error("Moto pod failed", exc_info=e)
        raise

    logger.info("✓ Moto pod is ready")
    moto_endpoint = f"http://moto.{test_namespace}.svc.cluster.local:5000"
    logger.info(f"✓ Moto service ready at {moto_endpoint}")
    return moto_endpoint


@pytest.fixture(scope="session")
def chart_name_or_full_name_override() -> str:
    return "s3-router"


@pytest.fixture(scope="session")
def _helm_chart_deployed(
    test_namespace: str,
    v1_api: CoreV1Api,
    apps_v1_api: AppsV1Api,
    moto_service: str,
    no_cleanup_cluster: bool,
) -> Iterator[str]:
    logger.info(f"Deploying helm chart to namespace {test_namespace}...")

    # Get the chart path
    script_dir = Path(__file__).parent
    chart_path = (script_dir / HELM_CHART_PATH).resolve()
    values_file = script_dir / TEST_VALUES_FILE

    if not chart_path.exists():
        raise SetupError(f"Helm chart not found at {chart_path}")

    if not values_file.exists():
        raise SetupError(f"Test values file not found at {values_file}")

    logger.info(f"Chart path: {chart_path}")
    logger.info(f"Values file: {values_file}")

    # Install helm chart
    cmd = [
        "helm",
        "upgrade",
        "--install",
        "-n",
        test_namespace,
        "-f",
        str(values_file),
        "--wait",
        "--timeout",
        "30s",
        RELEASE_NAME,
        str(chart_path),
    ]

    logger.info(f"Running: {' '.join(cmd)}")
    returncode, stdout, stderr = run_command(cmd, timeout=900)  # 15 minutes

    if stdout:
        logger.info(f"Helm stdout:\n{stdout}")

    if returncode != 0:
        logger.error(f"Helm install failed with return code {returncode}")
        logger.error(f"Helm stderr:\n{stderr}")

        # Try to get pod status before bailing
        try:
            pods = v1_api.list_namespaced_pod(test_namespace)
            for pod in pods.items:
                logger.error(f"Pod status: {pod.metadata.name} - {pod.status.phase}")
                if pod.status.container_statuses:
                    for cs in pod.status.container_statuses:
                        logger.error(f"  Container {cs.name}: {cs.state}")
        except Exception as e:
            logger.error(f"Could not fetch pod status: {e}")

        raise SetupError(f"Failed to install helm chart: {stderr}")

    logger.info("✓ Helm chart installed")

    yield RELEASE_NAME

    if no_cleanup_cluster:
        return

    # Cleanup: uninstall helm chart
    logger.info(f"Uninstalling helm chart {RELEASE_NAME}...")
    cmd = ["helm", "uninstall", RELEASE_NAME, "-n", test_namespace]
    returncode, _, stderr = run_command(cmd, timeout=60)
    if returncode != 0:
        logger.warning(f"Failed to uninstall helm chart: {stderr}")
    else:
        logger.info("✓ Helm chart uninstalled")


@pytest.fixture(scope="session")
def helm_chart_deployed(
    test_namespace: str,
    v1_api: CoreV1Api,
    apps_v1_api: AppsV1Api,
    moto_service: str,
    _helm_chart_deployed: str,
) -> str:
    """Deploy helm chart and wait for readiness."""

    # Wait for deployment to be ready
    logger.info("Waiting for deployment to be ready...")

    def deployment_ready() -> bool:
        try:
            deployment = apps_v1_api.read_namespaced_deployment(RELEASE_NAME, test_namespace)
            if deployment.status.ready_replicas is None:
                logger.debug(f"Deployment replicas: ready={deployment.status.ready_replicas}, desired={deployment.spec.replicas}")
                return False
            return deployment.status.ready_replicas >= deployment.spec.replicas
        except ApiException as e:
            logger.debug(f"Error reading deployment: {e}")
            return False

    try:
        wait_for_condition(deployment_ready, timeout=300, interval=2)
        logger.info("✓ Deployment is ready")
    except SetupError:
        logger.error("Deployment did not become ready in time")
        # Show pod details
        try:
            pods = v1_api.list_namespaced_pod(test_namespace)
            for pod in pods.items:
                logger.error(f"Pod: {pod.metadata.name}")
                get_pod_details(v1_api, pod.metadata.name, test_namespace)
        except Exception as e:
            logger.error(f"Could not fetch pod details: {e}")
        raise

    # Wait for pods to be ready
    logger.info("Waiting for pods to be ready...")

    def pods_ready() -> bool:
        try:
            pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/instance={RELEASE_NAME}")
            if not pods.items:
                logger.debug(f"No pods found with label app.kubernetes.io/instance={RELEASE_NAME}")
                return False
            for pod in pods.items:
                logger.debug(f"Pod {pod.metadata.name}: phase={pod.status.phase}")
                if pod.status.phase != "Running":
                    return False
            return True
        except ApiException as e:
            logger.debug(f"Error listing pods: {e}")
            return False

    try:
        wait_for_condition(pods_ready, timeout=300, interval=2)
        logger.info("✓ All pods are ready")
    except SetupError:
        logger.error("Pods did not become ready in time")
        # Show pod details
        try:
            pods = v1_api.list_namespaced_pod(test_namespace)
            for pod in pods.items:
                logger.error(f"Pod: {pod.metadata.name}")
                get_pod_details(v1_api, pod.metadata.name, test_namespace)
        except Exception as e:
            logger.error(f"Could not fetch pod details: {e}")
        raise

    return _helm_chart_deployed


@pytest.fixture
def s3_client(
    helm_chart_deployed: str,
    v1_api: CoreV1Api,
    test_namespace: str,
    moto_service: str,
) -> Iterator[S3Client]:
    """Create boto3 S3 client pointing to s3-router via port-forward.

    Uses Kubernetes Python client's portforward to expose the s3-router
    service from the Kubernetes cluster.

    Args:
        helm_chart_deployed: Ensures s3-router is running in Kubernetes
        v1_api: Kubernetes API client
        test_namespace: Test namespace
        moto_service: Moto service endpoint (ensures moto is ready)
    """
    with kubernetes_portforward(v1_api, test_namespace):
        endpoint_url = f"http://{RELEASE_NAME}.k8s:8080"
        logger.info(f"Creating S3 client pointing to {endpoint_url}")

        s3 = boto3.client(
            "s3",
            endpoint_url=endpoint_url,
            aws_access_key_id="test",
            aws_secret_access_key="test",
            region_name="us-east-1",
            verify=False,
        )

        yield s3


@pytest.fixture
def s3_with_buckets(
    s3_client: S3Client,
    v1_api: CoreV1Api,
    moto_service: str,
    test_namespace: str,
) -> Iterator[S3Client]:
    """Create required S3 buckets for tests.

    Creates buckets directly via moto service using Kubernetes port-forward.
    """
    with kubernetes_portforward(v1_api, test_namespace):
        moto_endpoint = "http://moto.k8s:5000"

        # Create boto3 client directly to moto service
        moto_client = boto3.client(
            "s3",
            endpoint_url=moto_endpoint,
            region_name="us-east-1",
            aws_access_key_id="testing",
            aws_secret_access_key="testing",
        )

        # Create test buckets
        buckets = [
            "test-bucket",
            "backend-prod",
            "backend-staging",
            "backend-archive",
            "advanced-bucket",
            "resilience-bucket",
            "virtual-bucket",
            "prefixed-virtual-bucket",
        ]

        for bucket_name in buckets:
            try:
                moto_client.create_bucket(Bucket=bucket_name)
                logger.info(f"✓ Created bucket: {bucket_name}")
            except Exception as e:
                logger.info(f"Bucket {bucket_name} already exists or error: {e}")

        yield s3_client
