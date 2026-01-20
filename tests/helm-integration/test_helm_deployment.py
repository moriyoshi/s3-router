"""Helm integration tests using minikube and moto."""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING

import pytest

if TYPE_CHECKING:
    from kubernetes.client import AppsV1Api, CoreV1Api  # type: ignore[import]

logger = logging.getLogger(__name__)


class TestHelmDeployment:
    """Test helm chart deployment and pod readiness."""

    def test_deployment_exists(self, test_namespace: str, helm_chart_deployed: str, apps_v1_api: AppsV1Api) -> None:
        """Test that deployment was created successfully."""
        deployment = apps_v1_api.read_namespaced_deployment(
            helm_chart_deployed,
            test_namespace,
        )

        assert deployment is not None
        assert deployment.metadata.name == helm_chart_deployed
        assert deployment.spec.replicas == 1
        logger.info(f"✓ Deployment {helm_chart_deployed} exists")

    @pytest.mark.usefixtures("helm_chart_deployed")
    def test_pods_are_ready(self, test_namespace: str, chart_name_or_full_name_override: str, v1_api: CoreV1Api) -> None:
        """Test that pods are running and ready."""
        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/name={chart_name_or_full_name_override}")

        assert len(pods.items) > 0, "No pods found for deployment"

        for pod in pods.items:
            assert pod.status.phase == "Running", f"Pod {pod.metadata.name} is in {pod.status.phase} phase"

            # Check container readiness
            if pod.status.container_statuses:
                for container in pod.status.container_statuses:
                    assert container.ready, f"Container {container.name} is not ready"

            logger.info(f"✓ Pod {pod.metadata.name} is ready")

    @pytest.mark.usefixtures("helm_chart_deployed")
    def test_service_exists(self, test_namespace: str, chart_name_or_full_name_override: str, v1_api: CoreV1Api) -> None:
        """Test that service was created."""
        services = v1_api.list_namespaced_service(
            test_namespace, label_selector=f"app.kubernetes.io/name={chart_name_or_full_name_override}"
        )

        assert len(services.items) > 0, "No service found"

        service = services.items[0]
        assert service.spec.ports is not None

        # Check for main port and admin port
        ports = {p.name: p.port for p in service.spec.ports}
        assert "http" in ports or any(p.target_port == 8080 for p in service.spec.ports), "Main port (8080) not found in service"

        logger.info(f"✓ Service {service.metadata.name} exists with ports")

    @pytest.mark.usefixtures("helm_chart_deployed")
    def test_configmap_created(self, test_namespace: str, chart_name_or_full_name_override: str, v1_api: CoreV1Api) -> None:
        """Test that configmap was created with config."""
        configmaps = v1_api.list_namespaced_config_map(
            test_namespace, label_selector=f"app.kubernetes.io/name={chart_name_or_full_name_override}"
        )

        assert len(configmaps.items) > 0, "No configmap found"

        configmap = configmaps.items[0]
        assert "config.yaml" in configmap.data or "config" in configmap.data, "Config not found in configmap"

        logger.info(f"✓ ConfigMap {configmap.metadata.name} created")

    @pytest.mark.usefixtures("helm_chart_deployed")
    def test_service_account_created(self, test_namespace: str, chart_name_or_full_name_override: str, v1_api: CoreV1Api) -> None:
        """Test that service account was created."""
        service_accounts = v1_api.list_namespaced_service_account(
            test_namespace, label_selector=f"app.kubernetes.io/name={chart_name_or_full_name_override}"
        )

        assert len(service_accounts.items) > 0, "No service account found"
        logger.info("✓ ServiceAccount created")

    @pytest.mark.usefixtures("helm_chart_deployed")
    def test_pod_port_forwarding(self, test_namespace: str, chart_name_or_full_name_override: str, v1_api: CoreV1Api) -> None:
        """Test that pods have expected ports exposed."""
        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/name={chart_name_or_full_name_override}")

        pod = pods.items[0]

        # Check container ports
        for container in pod.spec.containers:
            ports = {p.name: p.container_port for p in container.ports}

            # Should have main port (8080) and admin port (9090)
            assert 8080 in ports.values(), "Main port 8080 not exposed"
            assert 9090 in ports.values(), "Admin port 9090 not exposed"

        logger.info("✓ Pod ports correctly configured")

    def test_deployment_replicas_match(self, test_namespace: str, helm_chart_deployed: str, apps_v1_api: AppsV1Api) -> None:
        """Test that deployment has correct number of replicas."""
        deployment = apps_v1_api.read_namespaced_deployment(helm_chart_deployed, test_namespace)

        # All replicas should be ready
        assert deployment.status.replicas == 1
        assert deployment.status.ready_replicas == 1
        assert deployment.status.available_replicas == 1

        logger.info("✓ All replicas are ready and available")


class TestPodHealth:
    """Test pod health and resource constraints."""

    @pytest.mark.usefixtures("helm_chart_deployed")
    def test_pod_resource_limits(self, test_namespace: str, chart_name_or_full_name_override: str, v1_api: CoreV1Api) -> None:
        """Test that resource limits are set on pods."""
        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/name={chart_name_or_full_name_override}")

        for pod in pods.items:
            for container in pod.spec.containers:
                assert container.resources.limits is not None, f"No resource limits set on {container.name}"
                assert container.resources.requests is not None, f"No resource requests set on {container.name}"

        logger.info("✓ Resource limits and requests configured")

    @pytest.mark.usefixtures("helm_chart_deployed")
    def test_pod_security_context(self, test_namespace: str, chart_name_or_full_name_override: str, v1_api: CoreV1Api) -> None:
        """Test that security context is applied."""
        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/name={chart_name_or_full_name_override}")

        pod = pods.items[0]

        # Check pod security context
        if pod.spec.security_context:
            assert pod.spec.security_context.run_as_non_root is True

        logger.info("✓ Security context applied")

    @pytest.mark.usefixtures("helm_chart_deployed")
    def test_pod_no_privileged_containers(self, test_namespace: str, chart_name_or_full_name_override: str, v1_api: CoreV1Api) -> None:
        """Test that containers are not running as privileged."""
        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/name={chart_name_or_full_name_override}")

        for pod in pods.items:
            for container in pod.spec.containers:
                if container.security_context:
                    assert container.security_context.privileged is not True, "Container is running as privileged"

        logger.info("✓ No privileged containers")
