"""
Integration tests for path-style routing.

These tests verify that path-style access works correctly (bucket in URL path)
and that different bucket names route to different backends.
"""

from __future__ import annotations

import json
import os
import socket
import subprocess
import sys
import tempfile
import time
from collections.abc import Callable, Iterator
from pathlib import Path
from typing import TYPE_CHECKING, TypedDict

import pytest
import yaml

if TYPE_CHECKING:
    from types_boto3_s3 import S3Client


class PathStyleRouterContext(TypedDict):
    """Context for router with path-style routing configuration."""

    router_url: str
    router_port: int
    admin_url: str
    moto_client: S3Client
    moto_endpoint: str


@pytest.fixture
def s3router_path_style(
    moto_server: str, s3router_binary: Path, create_s3_client: Callable[[str], S3Client]
) -> Iterator[PathStyleRouterContext]:
    """
    Start s3router with path-style routing configuration.

    Configures two buckets routing to different backends to test
    that path-style routing correctly extracts the bucket name
    from the URL path.
    """
    moto_endpoint = moto_server

    # Find available ports
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        router_port = s.getsockname()[1]

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        admin_port = s.getsockname()[1]

    router_url = f"http://127.0.0.1:{router_port}"

    # Create moto client and setup buckets
    moto_client: S3Client = create_s3_client(moto_endpoint)

    # Create backend buckets
    moto_client.create_bucket(Bucket="path-style-backend")
    moto_client.create_bucket(Bucket="virtual-host-backend")

    # Create test objects
    moto_client.put_object(
        Bucket="path-style-backend",
        Key="test-object.txt",
        Body=b"path-style-content",
    )
    moto_client.put_object(
        Bucket="virtual-host-backend",
        Key="test-object.txt",
        Body=b"virtual-host-content",
    )

    # Create config with virtual_hosts
    config = {
        "backends": {
            "path-style-backend": {
                "bucket": "path-style-backend",
                "endpoint": f"{moto_endpoint}/${{bucket}}",
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "virtual-host-backend": {
                "bucket": "virtual-host-backend",
                "endpoint": f"{moto_endpoint}/${{bucket}}",
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
        },
        "buckets": [
            {
                "name": "path-style-bucket",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "path-style-backend",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "virtual-bucket",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "virtual-host-backend",
                        "rewrite": [],
                    }
                ],
            },
        ],
        "virtual_hosts": {
            "enabled": False,
        },
        "credentials_store": "",  # Will be replaced
        "server": {
            "read_timeout": "15s",
            "write_timeout": "15s",
            "idle_timeout": "60s",
            "max_body_size": "4GB",
            "route_cache_size": 1000,
        },
        "auth": {
            "default_region": "us-east-1",
        },
    }

    credentials = {
        "testing": {
            "secret_key": "testing",
            "enabled": True,
        }
    }

    # Write config files
    config_fd, config_file = tempfile.mkstemp(suffix=".yaml")
    credentials_fd, credentials_file = tempfile.mkstemp(suffix=".json")
    log_fd, log_file_path = tempfile.mkstemp(suffix=".log")

    try:
        config["credentials_store"] = credentials_file

        with os.fdopen(config_fd, "w") as f:
            yaml.dump(config, f)

        with os.fdopen(credentials_fd, "w") as f:
            json.dump(credentials, f)

        os.close(log_fd)

        # Start s3router
        proc = None
        with open(log_file_path, "w") as log_file:
            proc = subprocess.Popen(
                [
                    s3router_binary,
                    "-config",
                    config_file,
                    "-listen",
                    f":{router_port}",
                    "-admin",
                    f":{admin_port}",
                    "-log-level",
                    "debug",
                ],
                stdout=log_file,
                stderr=subprocess.STDOUT,
            )

        # Wait for server to be ready
        max_retries = 30
        for _ in range(max_retries):
            try:
                with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
                    s.settimeout(1)
                    if s.connect_ex(("127.0.0.1", router_port)) == 0:
                        break
            except Exception:
                pass
            time.sleep(0.1)
        else:
            if proc:
                proc.terminate()
            with open(log_file_path, "r") as f:
                logs = f.read()
            raise RuntimeError(f"s3router failed to start.\nLogs:\n{logs}")

        yield {
            "router_url": router_url,
            "router_port": router_port,
            "admin_url": f"http://127.0.0.1:{admin_port}",
            "moto_client": moto_client,
            "moto_endpoint": moto_endpoint,
        }

    finally:
        if proc:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()

        # Print logs on failure for debugging
        if proc and proc.returncode not in [0, None, -15]:
            try:
                with open(log_file_path, "r") as f:
                    logs = f.read()
                    if logs:
                        print(f"\n=== Router logs ===\n{logs}\n===\n", file=sys.stderr)
            except Exception:
                pass

        for path in [config_file, credentials_file, log_file_path]:
            try:
                os.unlink(path)
            except Exception:
                pass


class TestPathStyleAccess:
    """Tests for path-style access (bucket in URL path)."""

    def test_path_style_via_boto3(self, s3router_path_style: PathStyleRouterContext, create_s3_client: Callable[[str], S3Client]) -> None:
        """Test path-style access via boto3 client."""
        router_url = s3router_path_style["router_url"]

        # Use path-style addressing
        client: S3Client = create_s3_client(router_url)

        response = client.get_object(Bucket="path-style-bucket", Key="test-object.txt")
        content = response["Body"].read()
        assert content == b"path-style-content"


class TestSecondBucketRouting:
    """Tests for path-style bucket access (both buckets should work via path-style)."""

    def test_virtual_bucket_via_path_style(
        self, s3router_path_style: PathStyleRouterContext, create_s3_client: Callable[[str], S3Client]
    ) -> None:
        """Test that virtual-bucket also works via path-style routing."""
        router_url = s3router_path_style["router_url"]

        client: S3Client = create_s3_client(router_url)

        # When we request virtual-bucket, it should route to virtual-host-backend
        response = client.get_object(Bucket="virtual-bucket", Key="test-object.txt")
        content = response["Body"].read()
        assert content == b"virtual-host-content"


class TestDifferentBucketsRouteToDifferentBackends:
    """Tests verifying different buckets route to different backends."""

    def test_path_style_and_virtual_buckets_have_different_content(
        self, s3router_path_style: PathStyleRouterContext, create_s3_client: Callable[[str], S3Client]
    ) -> None:
        """Test that path-style-bucket and virtual-bucket route to different backends."""
        router_url = s3router_path_style["router_url"]

        client: S3Client = create_s3_client(router_url)

        # Get from path-style-bucket -> should be "path-style-content"
        path_resp = client.get_object(Bucket="path-style-bucket", Key="test-object.txt")
        path_content = path_resp["Body"].read()

        # Get from virtual-bucket -> should be "virtual-host-content"
        virtual_resp = client.get_object(Bucket="virtual-bucket", Key="test-object.txt")
        virtual_content = virtual_resp["Body"].read()

        assert path_content == b"path-style-content"
        assert virtual_content == b"virtual-host-content"
        assert path_content != virtual_content
