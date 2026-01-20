"""Pytest configuration for integration tests."""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
from collections.abc import Callable, Iterator
from pathlib import Path
from typing import TYPE_CHECKING, TypedDict

import boto3
import filelock
import pytest
import xdist  # type: ignore[import]
import yaml
from botocore.config import Config
from moto.server import ThreadedMotoServer

if TYPE_CHECKING:
    from types_boto3_s3 import S3Client


class S3RouterWithMoto(TypedDict):
    """Configuration for s3router with moto backend."""

    router_url: str
    admin_url: str
    moto_endpoint: str


class RouterWithMotoContext(TypedDict):
    """Context for router with moto backend."""

    router_url: str
    admin_url: str
    moto_client: S3Client
    moto_endpoint: str


@pytest.fixture
def create_s3_client() -> Callable[[str], S3Client]:
    region_name = "us-east-1"
    path_style = True

    def _(endpoint_url: str) -> S3Client:
        """Create an S3 client with proper configuration for testing."""
        config = Config(s3={"addressing_style": "path" if path_style else "virtual"})
        return boto3.client(
            "s3",
            endpoint_url=endpoint_url,
            region_name=region_name,
            aws_access_key_id="testing",
            aws_secret_access_key="testing",
            config=config,
        )

    return _


def pytest_configure(config: pytest.Config) -> None:
    """Configure pytest."""
    config.addinivalue_line("markers", "integration: mark test as an integration test")


@pytest.fixture(scope="session")
def moto_enabled() -> bool:
    """Mark that moto is available for testing."""
    return True


@pytest.fixture
def moto_server() -> Iterator[str]:
    """
    Start moto S3 server on an automatically chosen port.

    Yields: base URL of the moto server
    """
    server = ThreadedMotoServer(port=0, verbose=False)
    server.start()
    try:
        _, port = server.get_host_and_port()
        endpoint = f"http://127.0.0.1:{port}"
        yield endpoint
    finally:
        server.stop()


@pytest.fixture(scope="session")
def project_root() -> Path:
    return Path(__file__).parent.parent.parent


@pytest.fixture(scope="session")
def integration_bin_dir(request: pytest.FixtureRequest, project_root: Path) -> Iterator[Path]:
    d = Path(__file__).parent / "bin"
    d.mkdir(exist_ok=True)
    yield d
    if xdist.is_xdist_controller(request):
        shutil.rmtree(d)


@pytest.fixture
def s3router_binary(integration_bin_dir: Path, project_root: Path, tmp_path_factory: pytest.TempPathFactory) -> Path:
    # Build s3router binary to tests/integration/bin
    binary = integration_bin_dir / "s3router"

    root_tmp_dir = tmp_path_factory.getbasetemp().parent
    with filelock.FileLock(root_tmp_dir / "lock"):
        # Build the binary if it doesn't exist
        if not binary.exists():
            result = subprocess.run(
                ["go", "build", "-o", str(binary), "./cmd/s3router"], cwd=str(project_root), capture_output=True, text=True
            )
            if result.returncode != 0:
                raise RuntimeError(f"Failed to build s3router: {result.stderr}")

    return binary


@pytest.fixture
def s3router_with_moto(moto_server: str, s3router_binary: Path) -> Iterator[S3RouterWithMoto]:
    """
    Start s3router pointing to moto S3 backend.

    Yields: dict with router_url, admin_url
    """
    moto_endpoint = moto_server

    # Find router port
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("", 0))
        router_port = s.getsockname()[1]

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("", 0))
        admin_port = s.getsockname()[1]

    router_url = f"http://127.0.0.1:{router_port}"

    # Create endpoint template for path-style addressing
    # The template includes ${bucket} and ${key} placeholders to force path-style
    endpoint_template = f"{moto_endpoint}/${{bucket}}/${{key}}"

    # Create temporary config pointing to moto
    config = {
        "backends": {
            "moto-s3-1": {
                "bucket": "test-bucket",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "moto-s3-2": {
                "bucket": "backend-1",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "moto-s3-3": {
                "bucket": "backend-2",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "moto-s3-4": {
                "bucket": "backend-prod",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "moto-s3-5": {
                "bucket": "backend-staging",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "moto-s3-6": {
                "bucket": "backend-archive",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "moto-s3-7": {
                "bucket": "advanced-bucket",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "moto-s3-8": {
                "bucket": "resilience-bucket",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "moto-s3-9": {
                "bucket": "virtual-bucket",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
            "moto-s3-prefix": {
                "bucket": "prefixed-backend-bucket",
                "prefix": "app/data/",
                "endpoint": endpoint_template,
                "credentials": {
                    "type": "inline",
                    "access_key_id": "testing",
                    "secret_access_key": "testing",
                },
            },
        },
        "buckets": [
            {
                "name": "test-bucket",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "moto-s3-1",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "backend-1",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "moto-s3-2",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "backend-2",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "moto-s3-3",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "backend-prod",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "moto-s3-4",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "backend-staging",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "moto-s3-5",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "backend-archive",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "moto-s3-6",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "advanced-bucket",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "moto-s3-7",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "resilience-bucket",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "moto-s3-8",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "virtual-bucket",
                "routes": [
                    {
                        "path": "(.*)",
                        "backend": "moto-s3-9",
                        "rewrite": [],
                    }
                ],
            },
            {
                "name": "prefixed-virtual-bucket",
                "routes": [
                    {
                        "path": "^files/(?P<rest>.*)",
                        "backend": "moto-s3-prefix",
                        "rewrite": [{"result": "$rest"}],
                    }
                ],
            },
        ],
        "logging": {
            "level": "debug",
        },
    }

    with tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False) as f:
        yaml.dump(config, f)
        config_file = f.name

    # Create temp credentials file
    credentials = {"testing": {"secret_key": "testing", "enabled": True, "created_at": "2024-01-01T00:00:00Z"}}
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump(credentials, f)
        credentials_file = f.name

    proc = None
    try:
        # Start s3router with stdout/stderr piped to test output
        proc = subprocess.Popen(
            [
                str(s3router_binary),
                "-config",
                config_file,
                "-credentials",
                credentials_file,
                "-listen",
                f"127.0.0.1:{router_port}",
                "-admin",
                f"127.0.0.1:{admin_port}",
            ],
            stdout=sys.stderr,  # Pipe router logs to test stderr for visibility
            stderr=subprocess.STDOUT,
        )

        # Wait for server to be ready
        max_retries = 30
        for _ in range(max_retries):
            try:
                with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
                    s.settimeout(1)
                    connect_result = s.connect_ex(("127.0.0.1", router_port))
                    if connect_result == 0:
                        break
            except Exception:
                pass
            time.sleep(0.1)
        else:
            if proc:
                proc.terminate()
            raise RuntimeError(f"s3router failed to start on port {router_port}")

        admin_url = f"http://127.0.0.1:{admin_port}"
        yield {
            "router_url": router_url,
            "admin_url": admin_url,
            "moto_endpoint": moto_endpoint,
        }

    finally:
        if proc:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()

        try:
            os.unlink(config_file)
        except Exception:
            pass
        try:
            os.unlink(credentials_file)
        except Exception:
            pass


@pytest.fixture
def moto_client(moto_server: str, create_s3_client: Callable[[str], S3Client]) -> S3Client:
    """Create a boto3 client pointing directly to moto."""
    return create_s3_client(moto_server)


@pytest.fixture
def router_client(s3router_with_moto: S3RouterWithMoto, create_s3_client: Callable[[str], S3Client]) -> S3Client:
    """Create a boto3 client pointing to the router."""
    return create_s3_client(s3router_with_moto["router_url"])
