"""Tests for helm chart backend options propagation to configmap."""

from __future__ import annotations

import subprocess
from pathlib import Path
from typing import Any

import pytest
import yaml


def helm_template(values: dict[str, Any], chart_path: Path) -> dict[str, Any]:
    """Render helm template with given values.

    Args:
        values: Values to pass to helm template
        chart_path: Path to the helm chart

    Returns:
        Parsed YAML output from helm template
    """
    # Write values to a temporary file
    import tempfile

    with tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False) as f:
        yaml.dump(values, f)
        values_file = f.name

    try:
        result = subprocess.run(
            ["helm", "template", "test-release", str(chart_path), "--values", values_file],
            capture_output=True,
            text=True,
            check=True,
        )

        # Parse all YAML documents
        documents = list(yaml.safe_load_all(result.stdout))

        # Create a dict indexed by kind/name for easy lookup
        resources: dict[str, Any] = {}
        for doc in documents:
            if doc and "kind" in doc:
                kind = doc["kind"]
                name = doc.get("metadata", {}).get("name", "unnamed")
                resources[f"{kind}/{name}"] = doc

        return resources
    finally:
        Path(values_file).unlink()


@pytest.fixture
def chart_path() -> Path:
    """Get path to the helm chart."""
    # Tests are in tests/helm-integration, chart is at repo root
    return Path(__file__).parent.parent.parent / "chart"


class TestBackendOptionsConfigMap:
    """Test that backend options are properly propagated to configmap."""

    def test_all_backend_options_rendered(self, chart_path: Path) -> None:
        """Test that all backend options are rendered in configmap."""
        values = {
            "config": {
                "backends": {
                    "test-backend": {
                        "endpoint": "s3.us-east-1.amazonaws.com",
                        "region": "us-west-2",
                        "bucket": "test-bucket",
                        "prefix": "test/",
                        "timeout": "30s",
                        "retries": 5,
                        "usePathStyle": True,
                        "useFips": True,
                        "useGlobalEndpoint": False,
                        "useDualStack": True,
                        "accelerate": False,
                        "credentials": {
                            "type": "default",
                        },
                    },
                },
            },
        }

        resources = helm_template(values, chart_path)

        # Find the configmap
        configmap = None
        for key, resource in resources.items():
            if resource["kind"] == "ConfigMap" and "config" in key:
                configmap = resource
                break

        assert configmap is not None, "ConfigMap not found in rendered templates"

        # Parse the config.yaml from the configmap
        config_yaml = configmap["data"]["config.yaml"]
        config = yaml.safe_load(config_yaml)

        # Verify backend config
        assert "backends" in config
        assert "test-backend" in config["backends"]

        backend = config["backends"]["test-backend"]

        # Check all options are present
        assert backend["endpoint"] == "s3.us-east-1.amazonaws.com"
        assert backend["region"] == "us-west-2"
        assert backend["bucket"] == "test-bucket"
        assert backend["prefix"] == "test/"
        assert backend["timeout"] == "30s"
        assert backend["retries"] == 5
        assert backend["use_path_style"] is True
        assert backend["use_fips"] is True
        assert backend["use_dual_stack"] is True
        assert backend["credentials"]["type"] == "default"

    def test_use_path_style_true(self, chart_path: Path) -> None:
        """Test that usePathStyle: true is rendered as use_path_style: true."""
        values = {
            "config": {
                "backends": {
                    "minio-backend": {
                        "endpoint": "minio.local:9000",
                        "bucket": "test-bucket",
                        "usePathStyle": True,
                        "credentials": {"type": "default"},
                    },
                },
            },
        }

        resources = helm_template(values, chart_path)

        configmap = next(r for r in resources.values() if r["kind"] == "ConfigMap" and "config" in r["metadata"]["name"])
        config = yaml.safe_load(configmap["data"]["config.yaml"])

        assert config["backends"]["minio-backend"]["use_path_style"] is True

    def test_use_path_style_false(self, chart_path: Path) -> None:
        """Test that usePathStyle: false is omitted (helm template behavior with 'if' conditionals)."""
        values = {
            "config": {
                "backends": {
                    "s3-backend": {
                        "endpoint": "s3.amazonaws.com",
                        "bucket": "test-bucket",
                        "usePathStyle": False,
                        "credentials": {"type": "default"},
                    },
                },
            },
        }

        resources = helm_template(values, chart_path)

        configmap = next(r for r in resources.values() if r["kind"] == "ConfigMap" and "config" in r["metadata"]["name"])
        config = yaml.safe_load(configmap["data"]["config.yaml"])

        # Helm templates with {{ if $backendConfig.usePathStyle }} only render truthy values
        # So false values are omitted, which is fine since the default is false anyway
        assert "use_path_style" not in config["backends"]["s3-backend"]

    def test_endpoint_options_all_true(self, chart_path: Path) -> None:
        """Test all endpoint options set to true."""
        values = {
            "config": {
                "backends": {
                    "aws-backend": {
                        "bucket": "test-bucket",
                        "region": "us-east-1",
                        "usePathStyle": True,
                        "useFips": True,
                        "useGlobalEndpoint": True,
                        "useDualStack": True,
                        "accelerate": True,
                        "credentials": {"type": "default"},
                    },
                },
            },
        }

        resources = helm_template(values, chart_path)

        configmap = next(r for r in resources.values() if r["kind"] == "ConfigMap" and "config" in r["metadata"]["name"])
        config = yaml.safe_load(configmap["data"]["config.yaml"])

        backend = config["backends"]["aws-backend"]
        assert backend["use_path_style"] is True
        assert backend["use_fips"] is True
        assert backend["use_global_endpoint"] is True
        assert backend["use_dual_stack"] is True
        assert backend["accelerate"] is True

    def test_endpoint_options_omitted_when_not_set(self, chart_path: Path) -> None:
        """Test that endpoint options are omitted when not set in values."""
        values = {
            "config": {
                "backends": {
                    "minimal-backend": {
                        "bucket": "test-bucket",
                        "credentials": {"type": "default"},
                    },
                },
            },
        }

        resources = helm_template(values, chart_path)

        configmap = next(r for r in resources.values() if r["kind"] == "ConfigMap" and "config" in r["metadata"]["name"])
        config = yaml.safe_load(configmap["data"]["config.yaml"])

        backend = config["backends"]["minimal-backend"]

        # These should not be present when not set in values
        assert "use_path_style" not in backend
        assert "use_fips" not in backend
        assert "use_global_endpoint" not in backend
        assert "use_dual_stack" not in backend
        assert "accelerate" not in backend
        assert "region" not in backend

    def test_multiple_backends_with_different_options(self, chart_path: Path) -> None:
        """Test multiple backends with different endpoint options."""
        values = {
            "config": {
                "backends": {
                    "aws-standard": {
                        "bucket": "aws-bucket",
                        "region": "us-east-1",
                        "useFips": True,
                        "credentials": {"type": "default"},
                    },
                    "minio-local": {
                        "endpoint": "minio:9000",
                        "bucket": "minio-bucket",
                        "usePathStyle": True,
                        "credentials": {"type": "default"},
                    },
                    "s3-accelerate": {
                        "bucket": "s3-bucket",
                        "region": "us-west-2",
                        "accelerate": True,
                        "useDualStack": True,
                        "credentials": {"type": "default"},
                    },
                },
            },
        }

        resources = helm_template(values, chart_path)

        configmap = next(r for r in resources.values() if r["kind"] == "ConfigMap" and "config" in r["metadata"]["name"])
        config = yaml.safe_load(configmap["data"]["config.yaml"])

        # Check aws-standard backend
        aws = config["backends"]["aws-standard"]
        assert aws["region"] == "us-east-1"
        assert aws["use_fips"] is True
        assert "use_path_style" not in aws

        # Check minio-local backend
        minio = config["backends"]["minio-local"]
        assert minio["endpoint"] == "minio:9000"
        assert minio["use_path_style"] is True
        assert "use_fips" not in minio

        # Check s3-accelerate backend
        s3_accel = config["backends"]["s3-accelerate"]
        assert s3_accel["accelerate"] is True
        assert s3_accel["use_dual_stack"] is True
        assert "use_path_style" not in s3_accel

    def test_region_propagation(self, chart_path: Path) -> None:
        """Test that region is properly propagated from values to config."""
        values = {
            "config": {
                "backends": {
                    "regional-backend": {
                        "bucket": "test-bucket",
                        "region": "eu-central-1",
                        "credentials": {"type": "default"},
                    },
                },
            },
        }

        resources = helm_template(values, chart_path)

        configmap = next(r for r in resources.values() if r["kind"] == "ConfigMap" and "config" in r["metadata"]["name"])
        config = yaml.safe_load(configmap["data"]["config.yaml"])

        assert config["backends"]["regional-backend"]["region"] == "eu-central-1"

    def test_timeout_and_retries_propagation(self, chart_path: Path) -> None:
        """Test that timeout and retries are properly propagated."""
        values = {
            "config": {
                "backends": {
                    "custom-timeout-backend": {
                        "bucket": "test-bucket",
                        "timeout": "120s",
                        "retries": 10,
                        "credentials": {"type": "default"},
                    },
                },
            },
        }

        resources = helm_template(values, chart_path)

        configmap = next(r for r in resources.values() if r["kind"] == "ConfigMap" and "config" in r["metadata"]["name"])
        config = yaml.safe_load(configmap["data"]["config.yaml"])

        backend = config["backends"]["custom-timeout-backend"]
        assert backend["timeout"] == "120s"
        assert backend["retries"] == 10
