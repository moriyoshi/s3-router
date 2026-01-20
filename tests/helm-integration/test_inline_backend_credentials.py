"""Tests for inline backend credentials rendering as secrets and pod mounting."""

from __future__ import annotations

import base64
import json
import logging
from pathlib import Path
from typing import TYPE_CHECKING, Iterator

import pytest
import yaml
from kubernetes.client import ApiException  # type: ignore[import]

from conftest import RELEASE_NAME, SetupError, get_pod_details, run_command, wait_for_condition

if TYPE_CHECKING:
    from kubernetes.base.stream.ws_client import WSClient  # type: ignore[import]
    from kubernetes.client import AppsV1Api, CoreV1Api

logger = logging.getLogger(__name__)

# Test values file with inline credentials
INLINE_CREDS_VALUES_FILE = "test-values-inline-creds.yaml"


def read_ws_client(ws_client: WSClient) -> str:
    buf: list[str] = []
    try:
        for _ in range(10):
            buf.append(ws_client.read_stdout())
            ws_client.update(1)
    finally:
        ws_client.close()

    return "".join(buf)


@pytest.fixture(scope="module")
def inline_creds_release_name() -> str:
    """Release name for inline credentials tests."""
    return f"{RELEASE_NAME}-inline"


@pytest.fixture(scope="module")
def inline_creds_helm_deployed(
    test_namespace: str,
    v1_api: CoreV1Api,
    apps_v1_api: AppsV1Api,
    moto_service: str,
    inline_creds_release_name: str,
    no_cleanup_cluster: bool,
) -> Iterator[str]:
    """Deploy helm chart with inline credentials and wait for readiness."""
    logger.info(f"Deploying helm chart with inline credentials to namespace {test_namespace}...")

    script_dir = Path(__file__).parent
    chart_path = (script_dir / "../../chart").resolve()
    values_file = script_dir / INLINE_CREDS_VALUES_FILE

    if not chart_path.exists():
        raise SetupError(f"Helm chart not found at {chart_path}")

    if not values_file.exists():
        raise SetupError(f"Test values file not found at {values_file}")

    # Install helm chart with inline credentials values
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
        inline_creds_release_name,
        str(chart_path),
    ]

    logger.info(f"Running: {' '.join(cmd)}")
    returncode, stdout, stderr = run_command(cmd, timeout=900)

    if stdout:
        logger.info(f"Helm stdout:\n{stdout}")

    if returncode != 0:
        logger.error(f"Helm install failed with return code {returncode}")
        logger.error(f"Helm stderr:\n{stderr}")
        raise SetupError(f"Failed to install helm chart: {stderr}")

    logger.info("✓ Helm chart with inline credentials installed")

    # Wait for deployment to be ready
    logger.info("Waiting for deployment to be ready...")

    def deployment_ready() -> bool:
        try:
            deployment = apps_v1_api.read_namespaced_deployment(inline_creds_release_name, test_namespace)
            if deployment.status.ready_replicas is None:
                return False
            return deployment.status.ready_replicas >= deployment.spec.replicas
        except ApiException:
            return False

    try:
        wait_for_condition(deployment_ready, timeout=300, interval=2)
        logger.info("✓ Deployment with inline credentials is ready")
    except SetupError:
        logger.error("Deployment did not become ready in time")
        try:
            pods = v1_api.list_namespaced_pod(test_namespace)
            for pod in pods.items:
                if inline_creds_release_name in pod.metadata.name:
                    get_pod_details(v1_api, pod.metadata.name, test_namespace)
        except Exception as e:
            logger.error(f"Could not fetch pod details: {e}")
        raise

    yield inline_creds_release_name

    if no_cleanup_cluster:
        return

    # Cleanup
    logger.info(f"Uninstalling helm chart {inline_creds_release_name}...")
    cmd = ["helm", "uninstall", inline_creds_release_name, "-n", test_namespace]
    returncode, _, stderr = run_command(cmd, timeout=60)
    if returncode != 0:
        logger.warning(f"Failed to uninstall helm chart: {stderr}")
    else:
        logger.info("✓ Helm chart uninstalled")


class TestInlineBackendCredentialsSecret:
    """Test that inline backend credentials are correctly rendered as secrets."""

    def test_secret_contains_backend_credential_keys(
        self,
        test_namespace: str,
        inline_creds_helm_deployed: str,
        v1_api: CoreV1Api,
    ) -> None:
        """Test that the secret contains backend-{name}.json keys for inline credentials."""
        secret_name = f"{inline_creds_helm_deployed}-credentials"
        secret = v1_api.read_namespaced_secret(secret_name, test_namespace)

        assert secret is not None
        assert secret.data is not None

        # Check that backend credential keys exist
        assert "backend-inline-backend1.json" in secret.data, f"backend-inline-backend1.json not found in secret {secret.data:r}"
        assert "backend-inline-backend2.json" in secret.data, f"backend-inline-backend2.json not found in secret {secret.data:r}"

        logger.info(f"✓ Secret {secret_name} contains backend credential keys")

    def test_backend_credential_content_is_correct(
        self,
        test_namespace: str,
        inline_creds_helm_deployed: str,
        v1_api: CoreV1Api,
    ) -> None:
        """Test that backend credential content is correctly encoded in secret."""
        secret_name = f"{inline_creds_helm_deployed}-credentials"
        secret = v1_api.read_namespaced_secret(secret_name, test_namespace)

        # Decode and verify backend1 credentials
        backend1_data = base64.b64decode(secret.data["backend-inline-backend1.json"]).decode("utf-8")
        backend1_creds = json.loads(backend1_data)

        assert backend1_creds["access_key_id"] == "AKIAIOSFODNN7EXAMPLE1"
        assert backend1_creds["secret_access_key"] == "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY1"
        assert backend1_creds.get("session_token", "") == ""

        # Decode and verify backend2 credentials (with session token)
        backend2_data = base64.b64decode(secret.data["backend-inline-backend2.json"]).decode("utf-8")
        backend2_creds = json.loads(backend2_data)

        assert backend2_creds["access_key_id"] == "AKIAIOSFODNN7EXAMPLE2"
        assert backend2_creds["secret_access_key"] == "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY2"
        assert backend2_creds["session_token"] == "FwoGZXIvYXdzEBYaDExxxxxxxxxx"

        logger.info("✓ Backend credential content is correctly encoded")


class TestInlineBackendCredentialsPodMount:
    """Test that pods correctly mount inline backend credentials."""

    def test_pod_has_backend_credentials_volume(
        self,
        test_namespace: str,
        inline_creds_helm_deployed: str,
        v1_api: CoreV1Api,
    ) -> None:
        """Test that the pod has a backend-credentials volume."""
        pods = v1_api.list_namespaced_pod(
            test_namespace,
            label_selector=f"app.kubernetes.io/instance={inline_creds_helm_deployed}",
        )

        assert len(pods.items) > 0, "No pods found for deployment"

        pod = pods.items[0]

        # Check for backend-credentials volume
        volume_names = [v.name for v in pod.spec.volumes]
        assert "backend-credentials" in volume_names, "backend-credentials volume not found in pod"

        logger.info(f"✓ Pod {pod.metadata.name} has backend-credentials volume")

    def test_pod_mounts_backend_credentials_volume(
        self,
        test_namespace: str,
        inline_creds_helm_deployed: str,
        v1_api: CoreV1Api,
    ) -> None:
        """Test that the pod mounts the backend-credentials volume at correct path."""
        pods = v1_api.list_namespaced_pod(
            test_namespace,
            label_selector=f"app.kubernetes.io/instance={inline_creds_helm_deployed}",
        )

        pod = pods.items[0]
        container = pod.spec.containers[0]

        # Find the backend-credentials volume mount
        mount = None
        for vm in container.volume_mounts:
            if vm.name == "backend-credentials":
                mount = vm
                break

        assert mount is not None, "backend-credentials volume mount not found"
        assert mount.mount_path == "/etc/s3router/backend-credentials", f"Unexpected mount path: {mount.mount_path}"
        assert mount.read_only is True, "Volume should be mounted read-only"

        logger.info(f"✓ Pod mounts backend-credentials at {mount.mount_path}")

    def test_backend_credentials_volume_has_correct_items(
        self,
        test_namespace: str,
        inline_creds_helm_deployed: str,
        v1_api: CoreV1Api,
    ) -> None:
        """Test that backend-credentials volume references correct secret keys."""
        pods = v1_api.list_namespaced_pod(
            test_namespace,
            label_selector=f"app.kubernetes.io/instance={inline_creds_helm_deployed}",
        )

        pod = pods.items[0]

        # Find backend-credentials volume
        volume = None
        for v in pod.spec.volumes:
            if v.name == "backend-credentials":
                volume = v
                break

        assert volume is not None, "backend-credentials volume not found"
        assert volume.secret is not None, "Volume should reference a secret"

        # Check items are correctly mapped
        items = {item.key: item.path for item in volume.secret.items}
        assert "backend-inline-backend1.json" in items
        assert "backend-inline-backend2.json" in items
        assert items["backend-inline-backend1.json"] == "backend-inline-backend1.json"
        assert items["backend-inline-backend2.json"] == "backend-inline-backend2.json"

        logger.info("✓ Backend credentials volume has correct secret items")


class TestInlineBackendCredentialsConfigMap:
    """Test that ConfigMap correctly rewrites inline credentials to file type."""

    def test_configmap_rewrites_inline_to_file_type(
        self,
        test_namespace: str,
        inline_creds_helm_deployed: str,
        v1_api: CoreV1Api,
    ) -> None:
        """Test that ConfigMap rewrites inline credentials to file type with correct path."""
        configmap_name = f"{inline_creds_helm_deployed}-config"
        configmap = v1_api.read_namespaced_config_map(configmap_name, test_namespace)

        assert configmap is not None
        assert "config.yaml" in configmap.data

        config = yaml.safe_load(configmap.data["config.yaml"])

        # Check that inline credentials are rewritten to file type
        backends = config.get("backends", {})

        backend1_creds = backends["inline-backend1"]["credentials"]
        assert backend1_creds["type"] == "file", f"Expected type 'file', got '{backend1_creds['type']}'"
        assert backend1_creds["path"] == "/etc/s3router/backend-credentials/backend-inline-backend1.json"

        backend2_creds = backends["inline-backend2"]["credentials"]
        assert backend2_creds["type"] == "file", f"Expected type 'file', got '{backend2_creds['type']}'"
        assert backend2_creds["path"] == "/etc/s3router/backend-credentials/backend-inline-backend2.json"

        logger.info("✓ ConfigMap correctly rewrites inline credentials to file type")

    def test_configmap_does_not_contain_inline_secrets(
        self,
        test_namespace: str,
        inline_creds_helm_deployed: str,
        v1_api: CoreV1Api,
    ) -> None:
        """Test that ConfigMap does not contain the actual credential values."""
        configmap_name = f"{inline_creds_helm_deployed}-config"
        configmap = v1_api.read_namespaced_config_map(configmap_name, test_namespace)

        config_yaml = configmap.data["config.yaml"]

        # Ensure no secrets are in the configmap
        assert "AKIAIOSFODNN7EXAMPLE1" not in config_yaml, "Access key ID should not be in ConfigMap"
        assert "AKIAIOSFODNN7EXAMPLE2" not in config_yaml, "Access key ID should not be in ConfigMap"
        assert "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" not in config_yaml, "Secret key should not be in ConfigMap"
        assert "FwoGZXIvYXdzEBYaDExxxxxxxxxx" not in config_yaml, "Session token should not be in ConfigMap"

        logger.info("✓ ConfigMap does not contain inline secrets")


class TestCredentialFilesInPod:
    """Test that credential files are accessible inside the pod."""

    def test_credential_files_exist_in_pod(
        self,
        test_namespace: str,
        inline_creds_helm_deployed: str,
        v1_api: CoreV1Api,
    ) -> None:
        """Test that credential files are accessible inside the pod."""
        pods = v1_api.list_namespaced_pod(
            test_namespace,
            label_selector=f"app.kubernetes.io/instance={inline_creds_helm_deployed}",
        )

        pod = pods.items[0]
        pod_name = pod.metadata.name

        # Execute a command to list credential files
        exec_command = [
            "/busybox/ls",
            "-la",
            "/etc/s3router/backend-credentials/",
        ]

        from kubernetes.stream import stream  # type: ignore[import]

        output = read_ws_client(
            stream(
                v1_api.connect_get_namespaced_pod_exec,
                pod_name,
                test_namespace,
                command=exec_command,
                stderr=True,
                stdin=False,
                stdout=True,
                tty=False,
                _preload_content=False,
            )
        )

        logger.info(f"Credential files in pod:\n{output}")

        assert "backend-inline-backend1.json" in output, f"backend-inline-backend1.json not found in pod {output}"
        assert "backend-inline-backend2.json" in output, f"backend-inline-backend2.json not found in pod {output}"

        logger.info("✓ Credential files exist in pod")

    def test_credential_file_content_is_readable(
        self,
        test_namespace: str,
        inline_creds_helm_deployed: str,
        v1_api: CoreV1Api,
    ) -> None:
        """Test that credential file content is readable and correct."""
        from kubernetes.stream import stream  # type: ignore[import]

        pods = v1_api.list_namespaced_pod(
            test_namespace,
            label_selector=f"app.kubernetes.io/instance={inline_creds_helm_deployed}",
        )

        pod = pods.items[0]
        pod_name = pod.metadata.name

        # Read backend1 credential file
        exec_command = ["/busybox/cat", "/etc/s3router/backend-credentials/backend-inline-backend1.json"]
        # exec_command = ["/busybox/cat", "/etc/s3router/config/config.yaml"]
        output = read_ws_client(
            stream(
                v1_api.connect_get_namespaced_pod_exec,
                pod_name,
                test_namespace,
                command=exec_command,
                stderr=True,
                stdin=False,
                stdout=True,
                tty=False,
                _preload_content=False,
            )
        )

        logger.info(f"Credential file in pod:\n{output}")

        creds = json.loads(output)
        assert creds["access_key_id"] == "AKIAIOSFODNN7EXAMPLE1"
        assert creds["secret_access_key"] == "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY1"

        logger.info("✓ Credential file content is readable and correct")
