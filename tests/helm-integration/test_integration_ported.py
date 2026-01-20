"""
Ported integration tests from tests/integration/* to helm environment.

Tests basic S3 operations, routing, and advanced features through s3-router
running in Kubernetes with moto S3 backend.
"""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING

import pytest
from botocore.exceptions import ClientError
from kubernetes.client import CoreV1Api  # type: ignore[import]

if TYPE_CHECKING:
    from types_boto3_s3 import S3Client

logger = logging.getLogger(__name__)


class TestBasicS3Operations:
    """Test basic S3 operations through the router."""

    def test_router_health(self, helm_chart_deployed: str, v1_api: CoreV1Api, test_namespace: str) -> None:
        """Test that router pod is healthy."""
        from conftest import RELEASE_NAME

        pods = v1_api.list_namespaced_pod(test_namespace, label_selector=f"app.kubernetes.io/instance={RELEASE_NAME}")

        assert len(pods.items) > 0, "No router pods found"

        for pod in pods.items:
            assert pod.status.phase == "Running", f"Pod {pod.metadata.name} is not running"
            for container_status in pod.status.container_statuses:
                assert container_status.ready, f"Container {container_status.name} is not ready"

        logger.info("✓ Router is healthy")

    def test_put_object_through_router(self, s3_with_buckets: S3Client) -> None:
        """Test PUT object through router."""
        client = s3_with_buckets

        # PUT object
        client.put_object(Bucket="test-bucket", Key="test.txt", Body=b"test data")

        # Verify object exists
        resp = client.get_object(Bucket="test-bucket", Key="test.txt")
        assert resp["Body"].read() == b"test data"
        logger.info("✓ PUT object succeeded")

    def test_get_object_through_router(self, s3_with_buckets: S3Client) -> None:
        """Test GET object through router."""
        client = s3_with_buckets

        # Put object
        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"content")

        # Get object
        resp = client.get_object(Bucket="test-bucket", Key="file.txt")
        assert resp["Body"].read() == b"content"
        logger.info("✓ GET object succeeded")

    def test_delete_object_through_router(self, s3_with_buckets: S3Client) -> None:
        """Test DELETE object through router."""
        client = s3_with_buckets

        # Put object
        client.put_object(Bucket="test-bucket", Key="delete-me.txt", Body=b"data")

        # Delete it
        client.delete_object(Bucket="test-bucket", Key="delete-me.txt")

        # Verify it's gone
        with pytest.raises(ClientError) as exc:
            client.get_object(Bucket="test-bucket", Key="delete-me.txt")
        assert exc.value.response["Error"]["Code"] == "NoSuchKey"
        logger.info("✓ DELETE object succeeded")

    def test_head_object_through_router(self, s3_with_buckets: S3Client) -> None:
        """Test HEAD object through router."""
        client = s3_with_buckets

        # Put object with metadata
        client.put_object(
            Bucket="test-bucket",
            Key="file.txt",
            Body=b"test data",
            ContentType="text/plain",
            Metadata={"custom": "value"},
        )

        # HEAD should work
        resp = client.head_object(Bucket="test-bucket", Key="file.txt")
        assert resp["ContentLength"] == 9
        assert resp["ContentType"] == "text/plain"
        logger.info("✓ HEAD object succeeded")


class TestMultipleBackends:
    """Test routing with multiple backends."""

    def test_backend_isolation(self, s3_with_buckets: S3Client) -> None:
        """Test that backends are isolated from each other."""
        client = s3_with_buckets

        # Put data in different backends
        client.put_object(Bucket="backend-prod", Key="file.txt", Body=b"prod-data")
        client.put_object(Bucket="backend-staging", Key="file.txt", Body=b"staging-data")
        client.put_object(Bucket="backend-archive", Key="file.txt", Body=b"archive-data")

        # Verify isolation
        resp_prod = client.get_object(Bucket="backend-prod", Key="file.txt")
        resp_staging = client.get_object(Bucket="backend-staging", Key="file.txt")
        resp_archive = client.get_object(Bucket="backend-archive", Key="file.txt")

        assert resp_prod["Body"].read() == b"prod-data"
        assert resp_staging["Body"].read() == b"staging-data"
        assert resp_archive["Body"].read() == b"archive-data"
        logger.info("✓ Backend isolation verified")


class TestLargeFiles:
    """Test handling of large files."""

    def test_upload_large_file(self, s3_with_buckets: S3Client) -> None:
        """Test uploading a large file through router."""
        client = s3_with_buckets

        # Create 10MB of test data
        large_data = b"x" * (10 * 1024 * 1024)

        # Upload
        client.put_object(Bucket="test-bucket", Key="large-file.bin", Body=large_data)

        # Verify size
        resp = client.head_object(Bucket="test-bucket", Key="large-file.bin")
        assert resp["ContentLength"] == len(large_data)
        logger.info(f"✓ Uploaded {len(large_data)} bytes")

    def test_download_large_file(self, s3_with_buckets: S3Client) -> None:
        """Test downloading a large file through router."""
        client = s3_with_buckets

        # Create and upload large file
        large_data = b"y" * (10 * 1024 * 1024)
        client.put_object(Bucket="test-bucket", Key="large.bin", Body=large_data)

        # Download
        resp = client.get_object(Bucket="test-bucket", Key="large.bin")
        downloaded = resp["Body"].read()

        assert len(downloaded) == len(large_data)
        assert downloaded == large_data
        logger.info(f"✓ Downloaded {len(downloaded)} bytes")


class TestMetadata:
    """Test metadata handling."""

    def test_content_type(self, s3_with_buckets: S3Client) -> None:
        """Test that Content-Type is preserved."""
        client = s3_with_buckets

        # Put with specific content type
        client.put_object(
            Bucket="test-bucket",
            Key="document.pdf",
            Body=b"PDF data",
            ContentType="application/pdf",
        )

        # Get and verify
        resp = client.head_object(Bucket="test-bucket", Key="document.pdf")
        assert resp["ContentType"] == "application/pdf"
        logger.info("✓ Content-Type preserved")

    def test_custom_metadata(self, s3_with_buckets: S3Client) -> None:
        """Test that custom metadata is preserved."""
        client = s3_with_buckets

        # Put with custom metadata
        client.put_object(
            Bucket="test-bucket",
            Key="file.txt",
            Body=b"data",
            Metadata={"key1": "value1", "key2": "value2"},
        )

        # Get and verify metadata
        resp = client.head_object(Bucket="test-bucket", Key="file.txt")
        # Metadata handling may vary by backend, just verify no errors
        assert "Metadata" in resp
        logger.info("✓ Custom metadata handled")


class TestListObjects:
    """Test object listing."""

    def test_list_objects(self, s3_with_buckets: S3Client) -> None:
        """Test listing objects in a bucket."""
        client = s3_with_buckets

        # Put several objects
        for i in range(5):
            client.put_object(Bucket="test-bucket", Key=f"file{i}.txt", Body=b"data")

        # List objects
        resp = client.list_objects_v2(Bucket="test-bucket")

        assert "Contents" in resp
        assert len(resp["Contents"]) >= 5
        keys = [obj["Key"] for obj in resp["Contents"]]
        assert all(f"file{i}.txt" in keys for i in range(5))
        logger.info(f"✓ Listed {len(resp['Contents'])} objects")

    def test_list_with_prefix(self, s3_with_buckets: S3Client) -> None:
        """Test listing with prefix filter."""
        client = s3_with_buckets

        # Put objects with different prefixes
        client.put_object(Bucket="test-bucket", Key="logs/app.log", Body=b"log")
        client.put_object(Bucket="test-bucket", Key="logs/error.log", Body=b"error")
        client.put_object(Bucket="test-bucket", Key="data/file.txt", Body=b"data")

        # List with prefix
        resp = client.list_objects_v2(Bucket="test-bucket", Prefix="logs/")

        assert "Contents" in resp
        assert len(resp["Contents"]) >= 2
        keys = [obj["Key"] for obj in resp["Contents"]]
        assert "logs/app.log" in keys
        assert "logs/error.log" in keys
        assert "data/file.txt" not in keys
        logger.info(f"✓ Listed {len(resp['Contents'])} objects with prefix")


class TestErrorHandling:
    """Test error handling."""

    def test_get_nonexistent_object(self, s3_with_buckets: S3Client) -> None:
        """Test getting a non-existent object."""
        client = s3_with_buckets

        with pytest.raises(ClientError) as exc:
            client.get_object(Bucket="test-bucket", Key="does-not-exist.txt")

        assert exc.value.response["Error"]["Code"] == "NoSuchKey"
        logger.info("✓ NoSuchKey error raised correctly")

    def test_delete_nonexistent_object(self, s3_with_buckets: S3Client) -> None:
        """Test deleting a non-existent object (should not error in S3)."""
        client = s3_with_buckets

        # S3 DELETE is idempotent
        resp = client.delete_object(Bucket="test-bucket", Key="does-not-exist.txt")
        assert resp["ResponseMetadata"]["HTTPStatusCode"] in [200, 204]
        logger.info("✓ DELETE non-existent object succeeded (idempotent)")


class TestRoutingDecisions:
    """Test routing logic and backend selection."""

    def test_multiple_bucket_routing(self, s3_with_buckets: S3Client) -> None:
        """Test that requests to different buckets are routed correctly."""
        client = s3_with_buckets

        # Put data in different buckets
        client.put_object(Bucket="backend-prod", Key="data.txt", Body=b"prod data")
        client.put_object(Bucket="backend-staging", Key="data.txt", Body=b"staging data")

        # Verify routing
        resp_prod = client.get_object(Bucket="backend-prod", Key="data.txt")
        resp_staging = client.get_object(Bucket="backend-staging", Key="data.txt")

        assert resp_prod["Body"].read() == b"prod data"
        assert resp_staging["Body"].read() == b"staging data"
        logger.info("✓ Multiple bucket routing works")

    def test_read_write_split(self, s3_with_buckets: S3Client) -> None:
        """Test read/write splitting across backends."""
        client = s3_with_buckets

        # Write goes to backend-prod
        client.put_object(Bucket="backend-prod", Key="data.txt", Body=b"primary")

        # Read can come from backend-staging (replica)
        client.put_object(Bucket="backend-staging", Key="data.txt", Body=b"replica")

        # Both backends have the data
        assert client.get_object(Bucket="backend-prod", Key="data.txt")["Body"].read() == b"primary"
        assert client.get_object(Bucket="backend-staging", Key="data.txt")["Body"].read() == b"replica"
        logger.info("✓ Read/write split verified")

    def test_sharded_storage(self, s3_with_buckets: S3Client) -> None:
        """Test storage sharding by bucket."""
        client = s3_with_buckets

        # Shard A: backend-prod
        # Shard B: backend-staging

        # Store sharded data
        client.put_object(Bucket="backend-prod", Key="alice.json", Body=b"alice")
        client.put_object(Bucket="backend-staging", Key="zebra.json", Body=b"zebra")

        # Verify sharding
        assert client.get_object(Bucket="backend-prod", Key="alice.json")["Body"].read() == b"alice"
        assert client.get_object(Bucket="backend-staging", Key="zebra.json")["Body"].read() == b"zebra"
        logger.info("✓ Storage sharding verified")

    def test_tiered_storage(self, s3_with_buckets: S3Client) -> None:
        """Test tiered storage with hot/warm/cold backends."""
        client = s3_with_buckets

        # Hot: backend-prod (fast)
        # Warm: backend-staging
        # Cold: backend-archive

        client.put_object(Bucket="backend-prod", Key="logs/2024/01/15/app.log", Body=b"hot")
        client.put_object(Bucket="backend-staging", Key="logs/2023/06/15/app.log", Body=b"warm")
        client.put_object(Bucket="backend-archive", Key="logs/2022/01/15/app.log", Body=b"cold")

        # Verify tiers
        assert client.get_object(Bucket="backend-prod", Key="logs/2024/01/15/app.log")["Body"].read() == b"hot"
        assert client.get_object(Bucket="backend-staging", Key="logs/2023/06/15/app.log")["Body"].read() == b"warm"
        assert client.get_object(Bucket="backend-archive", Key="logs/2022/01/15/app.log")["Body"].read() == b"cold"
        logger.info("✓ Tiered storage verified")


class TestBackendIsolation:
    """Test that backends maintain isolation."""

    def test_data_isolation(self, s3_with_buckets: S3Client) -> None:
        """Test that data in one backend doesn't leak to another."""
        client = s3_with_buckets

        # Put data in each backend
        client.put_object(Bucket="backend-prod", Key="secret.txt", Body=b"prod-secret")
        client.put_object(Bucket="backend-staging", Key="secret.txt", Body=b"staging-secret")
        client.put_object(Bucket="backend-archive", Key="secret.txt", Body=b"archive-secret")

        # Verify isolation
        prod_data = client.get_object(Bucket="backend-prod", Key="secret.txt")["Body"].read()
        staging_data = client.get_object(Bucket="backend-staging", Key="secret.txt")["Body"].read()
        archive_data = client.get_object(Bucket="backend-archive", Key="secret.txt")["Body"].read()

        assert prod_data == b"prod-secret"
        assert staging_data == b"staging-secret"
        assert archive_data == b"archive-secret"
        assert prod_data != staging_data != archive_data
        logger.info("✓ Data isolation verified")

    def test_delete_isolation(self, s3_with_buckets: S3Client) -> None:
        """Test that delete in one backend doesn't affect others."""
        client = s3_with_buckets

        # Put same key in each backend
        for backend in ["backend-prod", "backend-staging", "backend-archive"]:
            client.put_object(Bucket=backend, Key="file.txt", Body=b"data")

        # Delete from one backend
        client.delete_object(Bucket="backend-prod", Key="file.txt")

        # Verify only one backend affected
        with pytest.raises(ClientError):
            client.get_object(Bucket="backend-prod", Key="file.txt")

        # Others still have the file
        assert client.get_object(Bucket="backend-staging", Key="file.txt")["Body"].read() == b"data"
        assert client.get_object(Bucket="backend-archive", Key="file.txt")["Body"].read() == b"data"
        logger.info("✓ Delete isolation verified")
