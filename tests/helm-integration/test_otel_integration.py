"""OpenTelemetry integration tests using stdout exporters."""

from __future__ import annotations

import logging
import time
from pathlib import Path
from typing import TYPE_CHECKING, Iterator

import boto3
import pytest
from kubernetes.client import ApiException  # type: ignore[import]

if TYPE_CHECKING:
    from kubernetes.client import AppsV1Api, CoreV1Api  # type: ignore[import]
    from types_boto3_s3 import S3Client

from conftest import kubernetes_portforward, run_command

logger = logging.getLogger(__name__)

TEST_NAMESPACE = "s3-router-test"
RELEASE_NAME = "s3-router-test"
HELM_CHART_PATH = "../../chart"
TEST_VALUES_FILE = "test-values-otel-stdout.yaml"


@pytest.fixture(scope="session")
def _helm_chart_deployed_otel(
    test_namespace: str,
    v1_api: CoreV1Api,
    apps_v1_api: AppsV1Api,
    moto_service: str,
    no_cleanup_cluster: bool,
) -> Iterator[str]:
    """Deploy helm chart with OTel stdout exporters for integration tests."""
    logger.info(f"Deploying helm chart with OTel stdout to namespace {test_namespace}...")

    # Get the chart path
    script_dir = Path(__file__).parent
    chart_path = (script_dir / HELM_CHART_PATH).resolve()
    values_file = script_dir / TEST_VALUES_FILE

    if not chart_path.exists():
        raise ValueError(f"Helm chart not found at {chart_path}")

    if not values_file.exists():
        raise ValueError(f"Test values file not found at {values_file}")

    logger.info(f"Chart path: {chart_path}")
    logger.info(f"Values file: {values_file}")

    # Use a different release name for OTel tests to avoid conflicts
    otel_release_name = f"{RELEASE_NAME}-otel"

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
        otel_release_name,
        str(chart_path),
    ]

    logger.info(f"Running: {' '.join(cmd)}")
    returncode, stdout, stderr = run_command(cmd, timeout=900)  # 15 minutes

    if stdout:
        logger.info(f"Helm stdout:\n{stdout}")

    if returncode != 0:
        logger.error(f"Helm install failed with return code {returncode}")
        logger.error(f"Helm stderr:\n{stderr}")
        raise ValueError(f"Failed to install helm chart: {stderr}")

    logger.info("✓ Helm chart with OTel stdout installed")

    yield otel_release_name

    if no_cleanup_cluster:
        return

    # Cleanup: uninstall helm chart
    logger.info(f"Uninstalling helm chart {otel_release_name}...")
    cmd = ["helm", "uninstall", otel_release_name, "-n", test_namespace]
    returncode, _, stderr = run_command(cmd, timeout=60)
    if returncode != 0:
        logger.warning(f"Failed to uninstall helm chart: {stderr}")
    else:
        logger.info("✓ Helm chart uninstalled")


@pytest.fixture(scope="session")
def helm_chart_deployed_otel_stdout(
    test_namespace: str,
    v1_api: CoreV1Api,
    apps_v1_api: AppsV1Api,
    moto_service: str,
    _helm_chart_deployed_otel: str,
) -> str:
    """Deploy helm chart and wait for readiness with OTel stdout exporters."""
    release_name = _helm_chart_deployed_otel

    # Wait for deployment to be ready
    logger.info("Waiting for deployment to be ready...")

    def deployment_ready() -> bool:
        try:
            deployment = apps_v1_api.read_namespaced_deployment(release_name, test_namespace)
            if deployment.status.ready_replicas is None:
                logger.debug(f"Deployment replicas: ready={deployment.status.ready_replicas}, desired={deployment.spec.replicas}")
                return False
            return deployment.status.ready_replicas >= deployment.spec.replicas
        except ApiException as e:
            logger.debug(f"Error reading deployment: {e}")
            return False

    # Wait for condition
    start_time = time.time()
    timeout = 300
    while time.time() - start_time < timeout:
        try:
            if deployment_ready():
                break
        except Exception as e:
            logger.debug(f"Condition check failed: {e}")
        time.sleep(2)
    else:
        raise ValueError(f"Deployment did not become ready within {timeout} seconds")

    logger.info("✓ Deployment is ready")

    return release_name


@pytest.fixture
def s3_client_otel(
    helm_chart_deployed_otel_stdout: str,
    v1_api: CoreV1Api,
    test_namespace: str,
    moto_service: str,
) -> Iterator[S3Client]:
    """Create boto3 S3 client pointing to OTel-enabled s3-router via port-forward.

    Args:
        helm_chart_deployed_otel_stdout: OTel release name
        v1_api: Kubernetes API client
        test_namespace: Test namespace
        moto_service: Moto service endpoint (ensures moto is ready)
    """
    release_name = helm_chart_deployed_otel_stdout

    with kubernetes_portforward(v1_api, test_namespace):
        endpoint_url = f"http://{release_name}.k8s:8080"
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


class TestOpenTelemetryIntegration:
    """Test OpenTelemetry integration with stdout exporters."""

    @pytest.mark.usefixtures("helm_chart_deployed_otel_stdout")
    def test_otel_environment_variables_set(self, test_namespace: str, helm_chart_deployed_otel_stdout: str, v1_api: CoreV1Api) -> None:
        """Test that OTel environment variables are correctly set in deployment."""
        release_name = helm_chart_deployed_otel_stdout
        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/instance={release_name}")

        assert len(pods.items) > 0, "No pods found for deployment"

        pod = pods.items[0]
        container = pod.spec.containers[0]

        # Check that OTel environment variables are set
        env_vars = {e.name: e.value for e in container.env if e.value}

        assert "OTEL_TRACES_EXPORTER" in env_vars, "OTEL_TRACES_EXPORTER not set"
        assert env_vars["OTEL_TRACES_EXPORTER"] == "stdout", (
            f"Expected OTEL_TRACES_EXPORTER=stdout, got {env_vars.get('OTEL_TRACES_EXPORTER')}"
        )

        assert "OTEL_METRICS_EXPORTER" in env_vars, "OTEL_METRICS_EXPORTER not set"
        assert env_vars["OTEL_METRICS_EXPORTER"] == "stdout", (
            f"Expected OTEL_METRICS_EXPORTER=stdout, got {env_vars.get('OTEL_METRICS_EXPORTER')}"
        )

        assert "OTEL_TRACES_SAMPLER" in env_vars, "OTEL_TRACES_SAMPLER not set"
        assert env_vars["OTEL_TRACES_SAMPLER"] == "always_on", "OTEL_TRACES_SAMPLER should be always_on for testing"

        logger.info("✓ OTel environment variables correctly configured")

    @pytest.mark.usefixtures("helm_chart_deployed_otel_stdout")
    def test_otel_service_name_configured(self, test_namespace: str, helm_chart_deployed_otel_stdout: str, v1_api: CoreV1Api) -> None:
        """Test that OTel service name is correctly configured."""
        release_name = helm_chart_deployed_otel_stdout
        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/instance={release_name}")

        assert len(pods.items) > 0, "No pods found for deployment"

        pod = pods.items[0]
        container = pod.spec.containers[0]

        # Check OTel service name
        env_vars = {e.name: e.value for e in container.env if e.value}

        assert "OTEL_SERVICE_NAME" in env_vars, "OTEL_SERVICE_NAME not set"
        assert env_vars["OTEL_SERVICE_NAME"] == "s3-router", f"Expected service name 's3-router', got {env_vars.get('OTEL_SERVICE_NAME')}"

        logger.info("✓ OTel service name correctly configured")

    @pytest.mark.usefixtures("helm_chart_deployed_otel_stdout")
    def test_pod_is_healthy_with_otel(self, test_namespace: str, helm_chart_deployed_otel_stdout: str, v1_api: CoreV1Api) -> None:
        """Test that pod is healthy and running with OTel enabled."""
        release_name = helm_chart_deployed_otel_stdout
        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/instance={release_name}")

        assert len(pods.items) > 0, "No pods found for deployment"

        pod = pods.items[0]
        assert pod.status.phase == "Running", f"Pod is in {pod.status.phase} phase, expected Running"

        # Check container readiness
        if pod.status.container_statuses:
            for container in pod.status.container_statuses:
                assert container.ready, f"Container {container.name} is not ready"

        logger.info("✓ Pod is healthy with OTel enabled")

    @pytest.mark.usefixtures("helm_chart_deployed_otel_stdout")
    def test_otel_traces_appear_in_stdout(
        self,
        test_namespace: str,
        helm_chart_deployed_otel_stdout: str,
        v1_api: CoreV1Api,
        s3_client_otel: S3Client,
    ) -> None:
        """Test that OTel traces are exported to stdout and visible in pod logs."""
        release_name = helm_chart_deployed_otel_stdout
        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/instance={release_name}")

        assert len(pods.items) > 0, "No pods found for deployment"

        pod = pods.items[0]
        pod_name = pod.metadata.name

        # Make a request to the s3-router to generate some trace data
        logger.info(f"Generating trace data by making S3 request via pod {pod_name}...")
        try:
            # Make an S3 API call to trigger tracing (list buckets is simple and always works)
            s3_client_otel.list_buckets()
            logger.info("✓ Successfully triggered trace via S3 API call")
        except Exception as e:
            logger.warning(f"Could not trigger trace via S3 request: {e}, checking for initialization traces...")

        # Wait a moment for logs to be written
        time.sleep(2)

        # Fetch pod logs
        try:
            logs = v1_api.read_namespaced_pod_log(name=pod_name, namespace=test_namespace, tail_lines=200)
        except Exception as e:
            logger.error(f"Failed to fetch pod logs: {e}")
            raise

        assert logs is not None and len(logs) > 0, "No logs found in pod"

        logger.info(f"Pod logs (first 500 chars):\n{logs[:500]}")

        # Check for OTel trace indicators in logs
        # The stdout exporter outputs trace data in specific formats
        has_trace_data = (
            '"traceID"' in logs
            or "traceID" in logs
            or "TraceID" in logs
            or '"trace_id"' in logs
            or "trace_id" in logs
            or '"spanID"' in logs
            or "spanID" in logs
            or "SpanID" in logs
            or '"span_id"' in logs
            or "span_id" in logs
            or "ResourceSpans" in logs
            or "ScopeSpans" in logs
            or "Span" in logs
            and "Attributes" in logs
        )

        assert has_trace_data, (
            f"No OTel trace data found in pod logs. Expected to see trace IDs, span IDs, or OTel span data. Logs: {logs[-1000:]}"
        )

        logger.info("✓ OTel traces found in pod stdout")
