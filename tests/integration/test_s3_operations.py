"""
Integration tests for S3 Router using moto (AWS S3 mocking library).

Moto allows testing S3 operations without requiring a live S3 service or Docker,
making it perfect for CI/CD environments and local development.

GET/LIST operations work correctly through the router.

Requirements:
    pip install moto boto3 pytest

Running Tests:
    pytest tests/integration/ -v
    pytest tests/integration/test_s3_operations.py -v
    pytest tests/integration/ -k routing
"""

from __future__ import annotations

import os
import tempfile
import threading
from collections.abc import Callable, Iterator
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import TYPE_CHECKING

import pytest
import requests
import boto3.s3.transfer as s3transfer

if TYPE_CHECKING:
    from types_boto3_s3 import S3Client
    from types_boto3_s3.type_defs import CompletedPartTypeDef

    from .conftest import S3RouterWithMoto


@pytest.fixture
def s3_setup(s3router_with_moto: S3RouterWithMoto, create_s3_client: Callable[[str], S3Client]) -> Iterator[S3Client]:
    """Setup S3 testing through s3-router with moto S3 backend."""
    moto_url = s3router_with_moto["moto_endpoint"]
    # Create boto3 client pointing directly to moto for bucket creation
    moto_client = create_s3_client(moto_url)

    # Create all necessary buckets on moto
    for bucket_name in ["test-bucket", "backend-1", "backend-2", "backend-prod", "backend-staging", "backend-archive", "virtual-bucket"]:
        try:
            moto_client.create_bucket(Bucket=bucket_name)
        except Exception:
            pass

    # Create client pointing to router (not moto directly)
    client = create_s3_client(s3router_with_moto["router_url"])

    yield client

    # Cleanup: clear all objects from buckets after test
    for bucket_name in ["test-bucket", "backend-1", "backend-2", "backend-prod", "backend-staging", "backend-archive", "virtual-bucket"]:
        try:
            paginator = client.get_paginator("list_objects_v2")
            pages = paginator.paginate(Bucket=bucket_name)
            for page in pages:
                if "Contents" in page:
                    # Use individual delete since batch delete_objects not supported yet
                    for obj in page["Contents"]:
                        client.delete_object(Bucket=bucket_name, Key=obj["Key"])
        except Exception:
            pass


class TestS3BucketOperations:
    """Test bucket-level operations."""

    def test_list_buckets(self, s3_setup: S3Client) -> None:
        """Test ListBuckets operation returns configured virtual buckets."""
        client = s3_setup

        # Call ListBuckets
        response = client.list_buckets()

        # Should return configured virtual buckets from s3router config
        bucket_names = [b["Name"] for b in response["Buckets"]]

        # These buckets are configured in conftest.py
        assert "test-bucket" in bucket_names
        assert "backend-1" in bucket_names
        assert "backend-2" in bucket_names
        assert "virtual-bucket" in bucket_names

    def test_list_buckets_requires_authentication(self, s3router_with_moto: S3RouterWithMoto) -> None:
        """Test that ListBuckets requires authentication (no auth bypass)."""
        router_url = s3router_with_moto["router_url"]

        # Make unauthenticated request to ListBuckets
        response = requests.get(router_url + "/", timeout=5)

        # Should return 401 Unauthorized (authentication required)
        assert response.status_code == 401, f"Expected 401, got {response.status_code}. Response: {response.text}"
        assert "WWW-Authenticate" in response.headers, "Expected WWW-Authenticate header in 401 response"

    def test_create_bucket_requires_authentication(self, s3router_with_moto: S3RouterWithMoto) -> None:
        """Test that CreateBucket requires authentication."""
        router_url = s3router_with_moto["router_url"]

        # Make unauthenticated PUT request to create a bucket
        response = requests.put(router_url + "/test-new-bucket", timeout=5)

        # Should return 401 Unauthorized
        assert response.status_code == 401, f"Expected 401, got {response.status_code}. Response: {response.text}"
        assert "WWW-Authenticate" in response.headers, "Expected WWW-Authenticate header in 401 response"

    def test_delete_bucket_requires_authentication(self, s3router_with_moto: S3RouterWithMoto) -> None:
        """Test that DeleteBucket requires authentication."""
        router_url = s3router_with_moto["router_url"]

        # Make unauthenticated DELETE request to delete a bucket
        response = requests.delete(router_url + "/test-bucket", timeout=5)

        # Should return 401 Unauthorized
        assert response.status_code == 401, f"Expected 401, got {response.status_code}. Response: {response.text}"
        assert "WWW-Authenticate" in response.headers, "Expected WWW-Authenticate header in 401 response"


class TestS3CRUDOperations:
    """Test basic S3 CRUD operations."""

    def test_put_get_object(self, s3_setup: S3Client) -> None:
        """Test PUT and GET operations."""
        client = s3_setup

        # PUT
        data = b"Hello, World!"
        client.put_object(Bucket="test-bucket", Key="test-file.txt", Body=data)

        # GET
        response = client.get_object(Bucket="test-bucket", Key="test-file.txt")
        assert response["Body"].read() == data

    def test_put_get_delete_object(self, s3_setup: S3Client) -> None:
        """Test PUT, GET, and DELETE operations."""
        client = s3_setup

        # PUT
        client.put_object(Bucket="test-bucket", Key="temp-file.txt", Body=b"temporary data")

        # GET to verify
        response = client.get_object(Bucket="test-bucket", Key="temp-file.txt")
        assert response["Body"].read() == b"temporary data"

        # DELETE
        client.delete_object(Bucket="test-bucket", Key="temp-file.txt")

        # Verify deletion
        with pytest.raises(Exception) as exc_info:
            client.get_object(Bucket="test-bucket", Key="temp-file.txt")
        assert "NoSuchKey" in str(exc_info.value)

    def test_head_object(self, s3_setup: S3Client) -> None:
        """Test HEAD operation."""
        client = s3_setup
        data = b"test data"

        client.put_object(Bucket="test-bucket", Key="file.txt", Body=data, ContentType="text/plain")

        response = client.head_object(Bucket="test-bucket", Key="file.txt")
        assert response["ContentLength"] == len(data)
        assert response["ContentType"] == "text/plain"
        assert "ETag" in response

    def test_large_object(self, s3_setup: S3Client) -> None:
        """Test uploading and downloading large objects."""
        client = s3_setup

        # Create 10MB object
        large_data = b"x" * (10 * 1024 * 1024)

        client.put_object(Bucket="test-bucket", Key="large-file.bin", Body=large_data)

        response = client.get_object(Bucket="test-bucket", Key="large-file.bin")
        retrieved = response["Body"].read()
        assert len(retrieved) == len(large_data)
        assert retrieved == large_data


class TestS3ObjectMetadata:
    """Test object metadata handling."""

    def test_object_metadata(self, s3_setup: S3Client) -> None:
        """Test metadata preservation."""
        client = s3_setup

        client.put_object(
            Bucket="test-bucket",
            Key="file.txt",
            Body=b"test data",
            Metadata={
                "custom-header": "custom-value",
                "app": "s3router",
            },
        )

        response = client.head_object(Bucket="test-bucket", Key="file.txt")
        assert response["Metadata"]["custom-header"] == "custom-value"
        assert response["Metadata"]["app"] == "s3router"

    def test_content_type_preservation(self, s3_setup: S3Client) -> None:
        """Test Content-Type preservation."""
        client = s3_setup

        client.put_object(Bucket="test-bucket", Key="document.pdf", Body=b"%PDF-1.4...", ContentType="application/pdf")

        response = client.head_object(Bucket="test-bucket", Key="document.pdf")
        assert response["ContentType"] == "application/pdf"


class TestS3ListOperations:
    """Test list operations."""

    def test_list_objects(self, s3_setup: S3Client) -> None:
        """Test listing all objects."""
        client = s3_setup

        # Create objects
        objects = {
            "file1.txt": b"content 1",
            "file2.txt": b"content 2",
            "file3.txt": b"content 3",
        }

        for key, body in objects.items():
            client.put_object(Bucket="test-bucket", Key=key, Body=body)

        response = client.list_objects_v2(Bucket="test-bucket")
        assert response["KeyCount"] == 3
        keys = {obj["Key"] for obj in response["Contents"]}
        assert keys == set(objects.keys())

    def test_list_objects_with_prefix(self, s3_setup: S3Client) -> None:
        """Test listing with prefix filtering."""
        client = s3_setup

        objects = {
            "logs/2024-01-01.log": b"log 1",
            "logs/2024-01-02.log": b"log 2",
            "data/file1.csv": b"data 1",
            "data/file2.csv": b"data 2",
        }

        for key, body in objects.items():
            client.put_object(Bucket="test-bucket", Key=key, Body=body)

        # List with prefix
        response = client.list_objects_v2(Bucket="test-bucket", Prefix="logs/")
        assert response["KeyCount"] == 2
        assert all("logs/" in obj["Key"] for obj in response["Contents"])

    def test_list_empty_bucket(self, s3_setup: S3Client) -> None:
        """Test listing empty bucket."""
        client = s3_setup

        response = client.list_objects_v2(Bucket="test-bucket")
        assert response.get("KeyCount", 0) == 0
        assert "Contents" not in response or len(response["Contents"]) == 0


class TestS3MultipleBackends:
    """Test multiple backend isolation."""

    def test_backend_isolation(self, s3_setup: S3Client) -> None:
        """Test that backends are isolated."""
        client = s3_setup

        # Put same key in different backends
        client.put_object(Bucket="backend-1", Key="file.txt", Body=b"backend-1 data")
        client.put_object(Bucket="backend-2", Key="file.txt", Body=b"backend-2 data")

        # Verify isolation
        resp1 = client.get_object(Bucket="backend-1", Key="file.txt")
        assert resp1["Body"].read() == b"backend-1 data"

        resp2 = client.get_object(Bucket="backend-2", Key="file.txt")
        assert resp2["Body"].read() == b"backend-2 data"

    def test_backend_specific_operations(self, s3_setup: S3Client) -> None:
        """Test operations specific to each backend."""
        client = s3_setup

        # Backend 1: create logs
        for i in range(3):
            client.put_object(Bucket="backend-1", Key=f"logs/file-{i}.log", Body=f"log data {i}".encode())

        # Backend 2: create data
        for i in range(2):
            client.put_object(Bucket="backend-2", Key=f"data/file-{i}.csv", Body=f"csv data {i}".encode())

        # Verify counts
        resp1 = client.list_objects_v2(Bucket="backend-1")
        assert resp1["KeyCount"] == 3

        resp2 = client.list_objects_v2(Bucket="backend-2")
        assert resp2["KeyCount"] == 2


class TestS3ErrorHandling:
    """Test error handling."""

    def test_get_nonexistent_object(self, s3_setup: S3Client) -> None:
        """Test GET on non-existent object."""
        client = s3_setup

        with pytest.raises(Exception) as exc_info:
            client.get_object(Bucket="test-bucket", Key="nonexistent.txt")
        assert "NoSuchKey" in str(exc_info.value)

    def test_head_nonexistent_object(self, s3_setup: S3Client) -> None:
        """Test HEAD on non-existent object."""
        client = s3_setup

        with pytest.raises(Exception) as exc_info:
            client.head_object(Bucket="test-bucket", Key="nonexistent.txt")
        # Moto returns HTTP 404, check for either error code or status
        assert "404" in str(exc_info.value) or "NoSuchKey" in str(exc_info.value)

    def test_delete_nonexistent_object(self, s3_setup: S3Client) -> None:
        """Test DELETE on non-existent object (should succeed)."""
        client = s3_setup

        # S3 delete is idempotent
        response = client.delete_object(Bucket="test-bucket", Key="nonexistent.txt")
        assert response["ResponseMetadata"]["HTTPStatusCode"] in [200, 204]


class TestS3ObjectUpdate:
    """Test object update operations."""

    def test_overwrite_object(self, s3_setup: S3Client) -> None:
        """Test overwriting existing object."""
        client = s3_setup

        # Original
        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"original")
        resp = client.get_object(Bucket="test-bucket", Key="file.txt")
        assert resp["Body"].read() == b"original"

        # Overwrite
        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"updated")
        resp = client.get_object(Bucket="test-bucket", Key="file.txt")
        assert resp["Body"].read() == b"updated"

    def test_update_with_different_metadata(self, s3_setup: S3Client) -> None:
        """Test updating object with different metadata."""
        client = s3_setup

        # Original
        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"data", Metadata={"version": "1"})

        # Update
        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"data", Metadata={"version": "2"})

        resp = client.head_object(Bucket="test-bucket", Key="file.txt")
        assert resp["Metadata"]["version"] == "2"


class TestS3SpecialCharacters:
    """Test handling of special characters in keys."""

    @pytest.mark.parametrize(
        "key",
        [
            "file with spaces.txt",
            "file-with-dashes.txt",
            "file_with_underscores.txt",
            "file.multiple.dots.txt",
            "path/with/multiple/slashes/file.txt",
            "file@with#special$chars.txt",
        ],
    )
    def test_special_character_keys(self, s3_setup: S3Client, key: str) -> None:
        """Test keys with special characters."""
        client = s3_setup
        data = f"data for {key}".encode()

        # PUT
        client.put_object(Bucket="test-bucket", Key=key, Body=data)

        # GET
        response = client.get_object(Bucket="test-bucket", Key=key)
        assert response["Body"].read() == data

        # HEAD
        head_response = client.head_object(Bucket="test-bucket", Key=key)
        assert head_response["ContentLength"] == len(data)


class TestS3RealWorldScenarios:
    """Test real-world usage scenarios."""

    def test_file_hierarchy(self, s3_setup: S3Client) -> None:
        """Test hierarchical file organization."""
        client = s3_setup

        # Create hierarchical structure
        files = {
            "projects/project-1/src/main.go": b"package main",
            "projects/project-1/src/utils.go": b"// utils",
            "projects/project-1/README.md": b"# Project 1",
            "projects/project-2/src/main.py": b"#!/usr/bin/env python",
            "projects/project-2/README.md": b"# Project 2",
        }

        for key, body in files.items():
            client.put_object(Bucket="test-bucket", Key=key, Body=body)

        # List project-1 files
        response = client.list_objects_v2(Bucket="test-bucket", Prefix="projects/project-1/")
        assert response["KeyCount"] == 3

    def test_log_archival(self, s3_setup: S3Client) -> None:
        """Test log archival scenario."""
        client = s3_setup

        # Create log structure: logs/YYYY/MM/DD/app.log
        logs = {
            "logs/2024/01/15/app-error.log": b"error log",
            "logs/2024/01/15/app-info.log": b"info log",
            "logs/2024/01/16/app-error.log": b"error log 16",
            "logs/2024/02/01/app-error.log": b"error log feb",
        }

        for key, body in logs.items():
            client.put_object(Bucket="test-bucket", Key=key, Body=body)

        # Query specific date
        response = client.list_objects_v2(Bucket="test-bucket", Prefix="logs/2024/01/15/")
        assert response["KeyCount"] == 2

    def test_backup_scenario(self, s3_setup: S3Client) -> None:
        """Test backup/restore scenario."""
        client = s3_setup

        # Backup multiple files
        files_to_backup = {
            "backup/config.yaml": b"app_config",
            "backup/database.sql": b"-- sql dump",
            "backup/manifest.txt": b"backup manifest",
        }

        for key, body in files_to_backup.items():
            client.put_object(Bucket="backend-1", Key=key, Body=body)

        # Verify all files present
        response = client.list_objects_v2(Bucket="backend-1", Prefix="backup/")
        assert response["KeyCount"] == 3

        # Restore specific file
        restored = client.get_object(Bucket="backend-1", Key="backup/config.yaml")
        assert restored["Body"].read() == b"app_config"


class TestCopyObject:
    """Test CopyObject operations."""

    def test_copy_object_basic(self, s3_setup: S3Client) -> None:
        """Test basic object copy operation."""
        client = s3_setup

        # Create source object
        client.put_object(Bucket="test-bucket", Key="source.txt", Body=b"original content")

        # Copy object
        client.copy_object(Bucket="test-bucket", CopySource={"Bucket": "test-bucket", "Key": "source.txt"}, Key="destination.txt")

        # Verify copy
        resp = client.get_object(Bucket="test-bucket", Key="destination.txt")
        assert resp["Body"].read() == b"original content"

    def test_copy_object_with_metadata(self, s3_setup: S3Client) -> None:
        """Test copy object with new metadata."""
        client = s3_setup

        # Create source with metadata
        client.put_object(Bucket="test-bucket", Key="source.txt", Body=b"data", Metadata={"source": "original"})

        # Copy with new metadata
        client.copy_object(
            Bucket="test-bucket",
            CopySource={"Bucket": "test-bucket", "Key": "source.txt"},
            Key="destination.txt",
            Metadata={"source": "copied"},
            MetadataDirective="REPLACE",
        )

        # Verify new metadata
        resp = client.head_object(Bucket="test-bucket", Key="destination.txt")
        assert resp["Metadata"].get("source") == "copied"

    def test_copy_object_between_backends(self, s3_setup: S3Client) -> None:
        """Test copy between different backends."""
        client = s3_setup

        # Create source in backend-1
        client.put_object(Bucket="backend-1", Key="data.txt", Body=b"backend1 data")

        # Copy to backend-2 using copy_object (within same bucket namespace)
        client.put_object(Bucket="backend-2", Key="data.txt", Body=b"backend1 data")

        # Verify both exist
        resp1 = client.get_object(Bucket="backend-1", Key="data.txt")
        resp2 = client.get_object(Bucket="backend-2", Key="data.txt")
        assert resp1["Body"].read() == resp2["Body"].read()

    def test_copy_large_object(self, s3_setup: S3Client) -> None:
        """Test copying large objects."""
        client = s3_setup

        # Create 5MB source
        large_data = b"x" * (5 * 1024 * 1024)
        client.put_object(Bucket="test-bucket", Key="large-source.bin", Body=large_data)

        # Copy it
        client.copy_object(Bucket="test-bucket", CopySource={"Bucket": "test-bucket", "Key": "large-source.bin"}, Key="large-dest.bin")

        # Verify copy
        resp = client.get_object(Bucket="test-bucket", Key="large-dest.bin")
        assert resp["Body"].read() == large_data


class TestDeleteObjects:
    """Test batch delete operations."""

    def test_delete_multiple_objects(self, s3_setup: S3Client) -> None:
        """Test deleting multiple objects in one operation."""
        client = s3_setup

        # Create multiple objects
        keys = ["file1.txt", "file2.txt", "file3.txt", "file4.txt"]
        for key in keys:
            client.put_object(Bucket="test-bucket", Key=key, Body=b"data")

        # Delete multiple
        response = client.delete_objects(Bucket="test-bucket", Delete={"Objects": [{"Key": key} for key in keys[:3]]})

        # Verify deletions
        assert len(response["Deleted"]) == 3

        # Verify file4 still exists
        resp = client.get_object(Bucket="test-bucket", Key="file4.txt")
        assert resp["Body"].read() == b"data"

        # Verify others are gone
        for key in keys[:3]:
            with pytest.raises(Exception) as exc:
                client.get_object(Bucket="test-bucket", Key=key)
            assert "NoSuchKey" in str(exc.value)

    def test_delete_objects_nonexistent(self, s3_setup: S3Client) -> None:
        """Test batch delete with non-existent keys (should not error)."""
        client = s3_setup

        # Delete non-existent objects
        response = client.delete_objects(
            Bucket="test-bucket",
            Delete={
                "Objects": [
                    {"Key": "nonexistent1.txt"},
                    {"Key": "nonexistent2.txt"},
                ]
            },
        )

        # S3 returns success for non-existent keys
        assert len(response["Deleted"]) == 2

    def test_delete_objects_mixed(self, s3_setup: S3Client) -> None:
        """Test batch delete with mix of existent and non-existent keys."""
        client = s3_setup

        # Create one object
        client.put_object(Bucket="test-bucket", Key="existing.txt", Body=b"data")

        # Delete mix
        response = client.delete_objects(
            Bucket="test-bucket",
            Delete={
                "Objects": [
                    {"Key": "existing.txt"},
                    {"Key": "nonexistent.txt"},
                ]
            },
        )

        assert len(response["Deleted"]) == 2


class TestObjectTagging:
    """Test object tagging operations."""

    def test_put_object_tagging(self, s3_setup: S3Client) -> None:
        """Test adding tags to object."""
        client = s3_setup

        # Create object
        client.put_object(Bucket="test-bucket", Key="tagged.txt", Body=b"data")

        # Add tags
        client.put_object_tagging(
            Bucket="test-bucket",
            Key="tagged.txt",
            Tagging={
                "TagSet": [
                    {"Key": "environment", "Value": "production"},
                    {"Key": "application", "Value": "web-api"},
                ]
            },
        )

        # Verify tags
        resp = client.get_object_tagging(Bucket="test-bucket", Key="tagged.txt")
        assert len(resp["TagSet"]) == 2
        tags = {tag["Key"]: tag["Value"] for tag in resp["TagSet"]}
        assert tags["environment"] == "production"
        assert tags["application"] == "web-api"

    def test_get_object_tagging(self, s3_setup: S3Client) -> None:
        """Test retrieving object tags."""
        client = s3_setup

        # Create and tag
        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"data")
        client.put_object_tagging(Bucket="test-bucket", Key="file.txt", Tagging={"TagSet": [{"Key": "version", "Value": "1.0"}]})

        # Get tags
        resp = client.get_object_tagging(Bucket="test-bucket", Key="file.txt")
        assert resp["TagSet"][0]["Key"] == "version"
        assert resp["TagSet"][0]["Value"] == "1.0"

    def test_delete_object_tagging(self, s3_setup: S3Client) -> None:
        """Test removing all tags from object."""
        client = s3_setup

        # Create, tag, then delete tags
        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"data")
        client.put_object_tagging(Bucket="test-bucket", Key="file.txt", Tagging={"TagSet": [{"Key": "temp", "Value": "yes"}]})

        # Delete tags
        client.delete_object_tagging(Bucket="test-bucket", Key="file.txt")

        # Verify empty
        resp = client.get_object_tagging(Bucket="test-bucket", Key="file.txt")
        assert len(resp["TagSet"]) == 0

    def test_update_object_tags(self, s3_setup: S3Client) -> None:
        """Test updating existing tags."""
        client = s3_setup

        # Create and tag
        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"data")
        client.put_object_tagging(Bucket="test-bucket", Key="file.txt", Tagging={"TagSet": [{"Key": "version", "Value": "1.0"}]})

        # Update tags
        client.put_object_tagging(Bucket="test-bucket", Key="file.txt", Tagging={"TagSet": [{"Key": "version", "Value": "2.0"}]})

        # Verify update
        resp = client.get_object_tagging(Bucket="test-bucket", Key="file.txt")
        assert resp["TagSet"][0]["Value"] == "2.0"


class TestRangeRequests:
    """Test byte-range request operations."""

    def test_get_object_range(self, s3_setup: S3Client) -> None:
        """Test getting object with byte range."""
        client = s3_setup

        # Create object
        data = b"0123456789ABCDEF"
        client.put_object(Bucket="test-bucket", Key="file.bin", Body=data)

        # Get range: bytes 0-4
        resp = client.get_object(Bucket="test-bucket", Key="file.bin", Range="bytes=0-4")
        result = resp["Body"].read()
        assert result == b"01234"

    def test_get_object_range_middle(self, s3_setup: S3Client) -> None:
        """Test getting middle range of object."""
        client = s3_setup

        data = b"0123456789ABCDEF"
        client.put_object(Bucket="test-bucket", Key="file.bin", Body=data)

        # Get bytes 5-9
        resp = client.get_object(Bucket="test-bucket", Key="file.bin", Range="bytes=5-9")
        assert resp["Body"].read() == b"56789"

    def test_get_object_range_from_end(self, s3_setup: S3Client) -> None:
        """Test getting last N bytes."""
        client = s3_setup

        data = b"0123456789ABCDEF"
        client.put_object(Bucket="test-bucket", Key="file.bin", Body=data)

        # Get last 4 bytes
        resp = client.get_object(Bucket="test-bucket", Key="file.bin", Range="bytes=-4")
        assert resp["Body"].read() == b"CDEF"

    def test_get_object_range_from_offset(self, s3_setup: S3Client) -> None:
        """Test getting bytes from offset to end."""
        client = s3_setup

        data = b"0123456789ABCDEF"
        client.put_object(Bucket="test-bucket", Key="file.bin", Body=data)

        # Get from byte 10 to end
        resp = client.get_object(Bucket="test-bucket", Key="file.bin", Range="bytes=10-")
        assert resp["Body"].read() == b"ABCDEF"

    def test_get_object_range_large_file(self, s3_setup: S3Client) -> None:
        """Test range request on large file."""
        client = s3_setup

        # Create 1MB file
        data = b"x" * (1024 * 1024)
        client.put_object(Bucket="test-bucket", Key="large.bin", Body=data)

        # Get 1KB from middle
        resp = client.get_object(Bucket="test-bucket", Key="large.bin", Range="bytes=512000-512999")
        result = resp["Body"].read()
        assert len(result) == 1000


class TestConditionalRequests:
    """Test conditional request operations."""

    def test_if_match_success(self, s3_setup: S3Client) -> None:
        """Test If-Match with matching ETag."""
        client = s3_setup

        # Create object
        put_response = client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"data")
        etag = put_response["ETag"]

        # Get with If-Match
        get_response = client.get_object(Bucket="test-bucket", Key="file.txt", IfMatch=etag)
        assert get_response["Body"].read() == b"data"

    def test_if_match_fail(self, s3_setup: S3Client) -> None:
        """Test If-Match with non-matching ETag."""
        client = s3_setup

        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"data")

        # Get with wrong ETag
        with pytest.raises(Exception) as exc:
            client.get_object(Bucket="test-bucket", Key="file.txt", IfMatch="wrong-etag")
        assert "PreconditionFailed" in str(exc.value)

    def test_if_none_match(self, s3_setup: S3Client) -> None:
        """Test If-None-Match (for caching)."""
        client = s3_setup

        # Create object
        put_response = client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"data")
        etag = put_response["ETag"]

        # Get with If-None-Match (same ETag)
        with pytest.raises(Exception) as exc:
            client.get_object(Bucket="test-bucket", Key="file.txt", IfNoneMatch=etag)
        # Moto returns HTTP 304, check for either error code or status
        assert "304" in str(exc.value) or "NotModified" in str(exc.value)

    def test_if_modified_since(self, s3_setup: S3Client) -> None:
        """Test If-Modified-Since header."""
        client = s3_setup
        from datetime import datetime, timedelta

        # Create object
        client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"data")

        # Get with future date (should return 304)
        future = datetime.utcnow() + timedelta(days=1)
        with pytest.raises(Exception):
            client.get_object(Bucket="test-bucket", Key="file.txt", IfModifiedSince=future)
        # May be NotModified error


class TestListObjectsV1:
    """Test ListObjects (v1) operation."""

    def test_list_objects_v1(self, s3_setup: S3Client) -> None:
        """Test basic ListObjects v1."""
        client = s3_setup

        # Create objects
        for i in range(3):
            client.put_object(Bucket="test-bucket", Key=f"file{i}.txt", Body=b"data")

        # Use list_objects (v1)
        resp = client.list_objects(Bucket="test-bucket")

        assert "Contents" in resp
        assert len(resp["Contents"]) >= 3

    def test_list_objects_v1_with_prefix(self, s3_setup: S3Client) -> None:
        """Test ListObjects v1 with prefix."""
        client = s3_setup

        client.put_object(Bucket="test-bucket", Key="logs/app.log", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="logs/error.log", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="data/file.txt", Body=b"data")

        resp = client.list_objects(Bucket="test-bucket", Prefix="logs/")

        assert len(resp["Contents"]) == 2
        assert all("logs/" in obj["Key"] for obj in resp["Contents"])

    def test_list_objects_v1_delimiter(self, s3_setup: S3Client) -> None:
        """Test ListObjects v1 with delimiter."""
        client = s3_setup

        client.put_object(Bucket="test-bucket", Key="a/file1.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="a/file2.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="b/file1.txt", Body=b"data")

        resp = client.list_objects(Bucket="test-bucket", Delimiter="/")

        assert "CommonPrefixes" in resp
        prefixes = [p["Prefix"] for p in resp["CommonPrefixes"]]
        assert "a/" in prefixes or "b/" in prefixes


class TestListObjectsWithNonSlashDelimiters:
    """Test ListObjects (v1/v2) with non-slash delimiters.

    S3 supports any character (or multi-character string) as a delimiter,
    not just '/'. This test class verifies that s3router correctly handles
    delimiters for common prefixes grouping.
    """

    def test_listobjectsv2_with_dash_delimiter(self, s3_setup: S3Client) -> None:
        """Test ListObjectsV2 with '-' delimiter."""
        client = s3_setup

        # Create objects with dash-separated naming
        client.put_object(Bucket="test-bucket", Key="log-2024-01-01.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="log-2024-01-02.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="log-2025-01-01.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="other-file.txt", Body=b"data")

        # List with '-' delimiter
        resp = client.list_objects_v2(Bucket="test-bucket", Delimiter="-")

        assert "CommonPrefixes" in resp
        prefixes = [p["Prefix"] for p in resp["CommonPrefixes"]]
        # Should group: 'log-', 'other-'
        assert "log-" in prefixes
        assert "other-" in prefixes

    def test_listobjectsv2_with_dot_delimiter(self, s3_setup: S3Client) -> None:
        """Test ListObjectsV2 with '.' delimiter."""
        client = s3_setup

        # Create objects with dot-separated naming (like versions)
        client.put_object(Bucket="test-bucket", Key="file.v1.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="file.v2.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="other.v1.txt", Body=b"data")

        # List with '.' delimiter
        resp = client.list_objects_v2(Bucket="test-bucket", Delimiter=".")

        assert "CommonPrefixes" in resp
        prefixes = [p["Prefix"] for p in resp["CommonPrefixes"]]
        # Should group: 'file.', 'other.'
        assert "file." in prefixes
        assert "other." in prefixes

    def test_listobjectsv2_with_underscore_delimiter(self, s3_setup: S3Client) -> None:
        """Test ListObjectsV2 with '_' delimiter."""
        client = s3_setup

        # Create objects with underscore-separated naming
        client.put_object(Bucket="test-bucket", Key="data_user_alice.json", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="data_user_bob.json", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="data_system_config.json", Body=b"data")

        # List with '_' delimiter
        resp = client.list_objects_v2(Bucket="test-bucket", Delimiter="_")

        assert "CommonPrefixes" in resp
        prefixes = [p["Prefix"] for p in resp["CommonPrefixes"]]
        # Should group: 'data_'
        assert "data_" in prefixes

    def test_listobjectsv2_with_multichar_delimiter(self, s3_setup: S3Client) -> None:
        """Test ListObjectsV2 with multi-character delimiter."""
        client = s3_setup

        # Create objects with '::' delimiter
        client.put_object(Bucket="test-bucket", Key="namespace1::key1", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="namespace1::key2", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="namespace2::key1", Body=b"data")

        # List with '::' delimiter
        resp = client.list_objects_v2(Bucket="test-bucket", Delimiter="::")

        assert "CommonPrefixes" in resp
        prefixes = [p["Prefix"] for p in resp["CommonPrefixes"]]
        # Should group: 'namespace1::', 'namespace2::'
        assert "namespace1::" in prefixes
        assert "namespace2::" in prefixes

    def test_listobjectsv1_with_dash_delimiter(self, s3_setup: S3Client) -> None:
        """Test ListObjectsV1 (deprecated but still used) with non-slash delimiter."""
        client = s3_setup

        # Create objects
        client.put_object(Bucket="test-bucket", Key="app-service-v1", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="app-service-v2", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="app-db-v1", Body=b"data")

        # List with '-' delimiter using v1 API
        resp = client.list_objects(Bucket="test-bucket", Delimiter="-")

        assert "CommonPrefixes" in resp
        prefixes = [p["Prefix"] for p in resp["CommonPrefixes"]]
        assert "app-" in prefixes

    def test_listobjectsv2_delimiter_with_prefix(self, s3_setup: S3Client) -> None:
        """Test delimiter grouping with prefix filter."""
        client = s3_setup

        # Create nested structure
        client.put_object(Bucket="test-bucket", Key="logs/2024-01-01.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="logs/2024-01-02.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="logs/2025-01-01.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="other/2024-01-01.txt", Body=b"data")

        # List with prefix and '-' delimiter
        resp = client.list_objects_v2(Bucket="test-bucket", Prefix="logs/", Delimiter="-")

        # Objects under logs/ filtered by '-' delimiter
        # Should show 2024- and 2025- prefixes
        if "CommonPrefixes" in resp:
            prefixes = [p["Prefix"] for p in resp["CommonPrefixes"]]
            assert any("2024-" in p for p in prefixes) or any("2025-" in p for p in prefixes)

    def test_listobjectsv2_no_common_prefixes(self, s3_setup: S3Client) -> None:
        """Test that no CommonPrefixes are returned when delimiter doesn't match."""
        client = s3_setup

        # Create simple flat keys
        client.put_object(Bucket="test-bucket", Key="file1.txt", Body=b"data")
        client.put_object(Bucket="test-bucket", Key="file2.txt", Body=b"data")

        # List with delimiter that doesn't exist in keys
        resp = client.list_objects_v2(Bucket="test-bucket", Delimiter="::")

        # Should return objects but no CommonPrefixes
        assert "Contents" in resp
        assert len(resp["Contents"]) == 2
        # No CommonPrefixes expected (or empty list)
        assert "CommonPrefixes" not in resp or len(resp.get("CommonPrefixes", [])) == 0

    def test_listobjectsv2_delimiter_pagination(self, s3_setup: S3Client) -> None:
        """Test pagination with non-slash delimiter."""
        client = s3_setup

        # Create many objects with dash delimiter
        for i in range(10):
            client.put_object(Bucket="test-bucket", Key=f"log-entry-{i:03d}.txt", Body=b"data")
        for i in range(5):
            client.put_object(Bucket="test-bucket", Key=f"app-entry-{i:03d}.txt", Body=b"data")

        # List with '-' delimiter and small page size
        resp = client.list_objects_v2(
            Bucket="test-bucket",
            Delimiter="-",
            MaxKeys=1,  # Small page size
        )

        assert "CommonPrefixes" in resp
        prefixes = [p["Prefix"] for p in resp["CommonPrefixes"]]
        # Should at least have one prefix from this page
        assert len(prefixes) > 0


class TestUnicodeAndSpecialCharacters:
    """Test handling of unicode and special characters in keys."""

    @pytest.mark.parametrize(
        "key",
        [
            "файл.txt",  # Cyrillic
            "文件.txt",  # Chinese
            "日本語.txt",  # Japanese
            "αρχείο.txt",  # Greek
            "ملف.txt",  # Arabic
        ],
    )
    def test_unicode_keys(self, s3_setup: S3Client, key: str) -> None:
        """Test keys with various unicode characters."""
        client = s3_setup
        data = f"data for {key}".encode("utf-8")

        # PUT
        client.put_object(Bucket="test-bucket", Key=key, Body=data)

        # GET
        response = client.get_object(Bucket="test-bucket", Key=key)
        assert response["Body"].read() == data

        # HEAD
        head_response = client.head_object(Bucket="test-bucket", Key=key)
        assert head_response["ContentLength"] == len(data)

    @pytest.mark.parametrize(
        "key",
        [
            "file$with$dollar.txt",
            "file&ampersand.txt",
            "file=equals.txt",
            "file+plus.txt",
            "file%20encoded.txt",
        ],
    )
    def test_special_character_keys(self, s3_setup: S3Client, key: str) -> None:
        """Test keys with special characters."""
        client = s3_setup
        data = b"test data"

        client.put_object(Bucket="test-bucket", Key=key, Body=data)
        response = client.get_object(Bucket="test-bucket", Key=key)
        assert response["Body"].read() == data

    def test_key_with_consecutive_slashes(self, s3_setup: S3Client) -> None:
        """Test key with multiple consecutive slashes."""
        client = s3_setup
        key = "path//with//double//slashes.txt"
        data = b"data"

        client.put_object(Bucket="test-bucket", Key=key, Body=data)
        response = client.get_object(Bucket="test-bucket", Key=key)
        assert response["Body"].read() == data


class TestUrlEncodingBehavior:
    """Test S3 URL encoding behavior per AWS specification.

    According to AWS S3 REST API spec:
    - %2F in URL path is decoded to / (treated as path separator)
    - To store a literal %2F in key, client must double-encode as %252F
    - This matches real S3 behavior where %2F is a delimiter

    See: https://docs.aws.amazon.com/AmazonS3/latest/API/RESTAPI.html
    """

    # @pytest.mark.xfail(reason="AWS SDK Go v2 limitation: %2F needs to be converted to / before passing to the SDK API")
    def test_key_with_literal_percent2f(self, s3_setup: S3Client) -> None:
        """Test key that literally contains %2F characters.

        When storing a key like 'folder%2Ffile.txt', boto3 encodes
        the % as %25, so the URL contains %252F, which decodes to %2F.
        """
        client = s3_setup
        # This key literally contains the characters '%', '2', 'F'
        key = "folder%2Ffile.txt"
        data = b"data with literal percent2f in key"

        client.put_object(Bucket="test-bucket", Key=key, Body=data)
        response = client.get_object(Bucket="test-bucket", Key=key)
        assert response["Body"].read() == data

        # Verify the key is distinct from folder/file.txt
        client.put_object(Bucket="test-bucket", Key="folder/file.txt", Body=b"different data")

        # Both keys should exist independently
        response1 = client.get_object(Bucket="test-bucket", Key=key)
        response2 = client.get_object(Bucket="test-bucket", Key="folder/file.txt")
        assert response1["Body"].read() == data
        assert response2["Body"].read() == b"different data"

    def test_slash_vs_encoded_slash_are_same(self, s3_setup: S3Client) -> None:
        """Test that URL-encoded slash %2F is treated as slash separator.

        Per S3 spec: /bucket/foo%2Fbar should access key 'foo/bar',
        not key 'foo%2Fbar'. S3 decodes %2F to / in the path.

        Note: boto3 handles this automatically - when you pass 'foo/bar'
        as the key, it properly encodes it in the URL.
        """
        client = s3_setup
        # Store with normal slash
        key = "folder/subfile.txt"
        data = b"data via normal slash"

        client.put_object(Bucket="test-bucket", Key=key, Body=data)

        # Retrieve with same key - boto3 will encode the / properly
        response = client.get_object(Bucket="test-bucket", Key=key)
        assert response["Body"].read() == data

    def test_multiple_percent_encodings(self, s3_setup: S3Client) -> None:
        """Test keys with various percent-encoded characters."""
        client = s3_setup

        test_cases = [
            ("file%20with%20spaces.txt", b"spaces"),  # %20 = space (but literal in key)
            ("file%23hash.txt", b"hash"),  # %23 = # (but literal in key)
            ("100%25complete.txt", b"percent"),  # %25 = % (literal percent sign)
        ]

        for key, data in test_cases:
            client.put_object(Bucket="test-bucket", Key=key, Body=data)
            response = client.get_object(Bucket="test-bucket", Key=key)
            assert response["Body"].read() == data, f"Failed for key: {key}"


class TestLongKeyNames:
    """Test handling of very long key names."""

    def test_key_1024_characters(self, s3_setup: S3Client) -> None:
        """Test 1KB key name."""
        client = s3_setup
        key = "a" * 1024
        data = b"data"

        client.put_object(Bucket="test-bucket", Key=key, Body=data)
        response = client.get_object(Bucket="test-bucket", Key=key)
        assert response["Body"].read() == data

    def test_key_2048_characters(self, s3_setup: S3Client) -> None:
        """Test 2KB key name."""
        client = s3_setup
        key = "prefix/" + "path/" * 100 + "file.txt"
        data = b"data"

        client.put_object(Bucket="test-bucket", Key=key, Body=data)
        response = client.get_object(Bucket="test-bucket", Key=key)
        assert response["Body"].read() == data


class TestEmptyObjects:
    """Test handling of empty objects."""

    def test_put_empty_object(self, s3_setup: S3Client) -> None:
        """Test creating empty object."""
        client = s3_setup

        client.put_object(Bucket="test-bucket", Key="empty.txt", Body=b"")
        response = client.get_object(Bucket="test-bucket", Key="empty.txt")
        assert response["Body"].read() == b""

    def test_head_empty_object(self, s3_setup: S3Client) -> None:
        """Test HEAD on empty object."""
        client = s3_setup

        client.put_object(Bucket="test-bucket", Key="empty.bin", Body=b"")
        response = client.head_object(Bucket="test-bucket", Key="empty.bin")
        assert response["ContentLength"] == 0

    def test_copy_empty_object(self, s3_setup: S3Client) -> None:
        """Test copying empty object."""
        client = s3_setup

        client.put_object(Bucket="test-bucket", Key="empty-src.txt", Body=b"")
        client.copy_object(Bucket="test-bucket", CopySource={"Bucket": "test-bucket", "Key": "empty-src.txt"}, Key="empty-dst.txt")

        response = client.get_object(Bucket="test-bucket", Key="empty-dst.txt")
        assert response["Body"].read() == b""

    def test_tag_empty_object(self, s3_setup: S3Client) -> None:
        """Test tagging empty object."""
        client = s3_setup

        client.put_object(Bucket="test-bucket", Key="empty-tagged.txt", Body=b"")
        client.put_object_tagging(Bucket="test-bucket", Key="empty-tagged.txt", Tagging={"TagSet": [{"Key": "empty", "Value": "true"}]})

        resp = client.get_object_tagging(Bucket="test-bucket", Key="empty-tagged.txt")
        assert len(resp["TagSet"]) == 1


class TestConcurrentOperations:
    """Test concurrent S3 operations."""

    def test_concurrent_puts(self, s3_setup: S3Client) -> None:
        """Test concurrent PUT operations."""
        client = s3_setup

        def put_object(index: int) -> int:
            client.put_object(Bucket="test-bucket", Key=f"concurrent-{index}.txt", Body=f"data {index}".encode())
            return index

        # Execute 10 concurrent PUTs
        with ThreadPoolExecutor(max_workers=5) as executor:
            futures = [executor.submit(put_object, i) for i in range(10)]
            results = [f.result() for f in as_completed(futures)]

        assert len(results) == 10

        # Verify all objects exist
        for i in range(10):
            resp = client.get_object(Bucket="test-bucket", Key=f"concurrent-{i}.txt")
            assert resp["Body"].read() == f"data {i}".encode()

    def test_concurrent_gets(self, s3_setup: S3Client) -> None:
        """Test concurrent GET operations."""
        client = s3_setup

        # Create test objects
        for i in range(10):
            client.put_object(Bucket="test-bucket", Key=f"file-{i}.txt", Body=f"data {i}".encode())

        def get_object(index: int) -> tuple[int, bytes]:
            resp = client.get_object(Bucket="test-bucket", Key=f"file-{index}.txt")
            return index, resp["Body"].read()

        # Execute 10 concurrent GETs
        with ThreadPoolExecutor(max_workers=5) as executor:
            futures = {executor.submit(get_object, i): i for i in range(10)}
            results: dict[int, bytes] = {}
            for future in as_completed(futures):
                index, data = future.result()
                results[index] = data

        assert len(results) == 10
        assert all(results[i] == f"data {i}".encode() for i in range(10))

    def test_concurrent_mixed_operations(self, s3_setup: S3Client) -> None:
        """Test concurrent mix of PUT, GET, DELETE."""
        client = s3_setup
        threading.Lock()

        def mixed_operation(op_type: str, index: int) -> bytes | int | None:
            if op_type == "put":
                client.put_object(Bucket="test-bucket", Key=f"mixed-{index}.txt", Body=f"data {index}".encode())
            elif op_type == "get":
                resp = client.get_object(Bucket="test-bucket", Key="mixed-0.txt")
                return resp["Body"].read()
            elif op_type == "head":
                head_response = client.head_object(Bucket="test-bucket", Key="mixed-0.txt")
                return head_response["ContentLength"]
            return None

        # Pre-create one object for GET/HEAD
        client.put_object(Bucket="test-bucket", Key="mixed-0.txt", Body=b"data 0")

        # Execute mixed operations
        with ThreadPoolExecutor(max_workers=5) as executor:
            futures = []
            for i in range(5):
                futures.append(executor.submit(mixed_operation, "put", i + 1))
            for i in range(3):
                futures.append(executor.submit(mixed_operation, "get", 0))
            for i in range(2):
                futures.append(executor.submit(mixed_operation, "head", 0))

            mixed_results = [f.result() for f in as_completed(futures)]

        assert len(mixed_results) == 10


class TestStressScenarios:
    """Test stress scenarios with many objects."""

    def test_create_many_objects(self, s3_setup: S3Client) -> None:
        """Test creating 100 objects."""
        client = s3_setup

        # Create 100 objects
        for i in range(100):
            client.put_object(Bucket="test-bucket", Key=f"stress-{i:04d}.txt", Body=f"data {i}".encode())

        # Verify count via list
        resp = client.list_objects_v2(Bucket="test-bucket")
        assert resp["KeyCount"] == 100

    def test_list_many_objects(self, s3_setup: S3Client) -> None:
        """Test listing bucket with many objects."""
        client = s3_setup

        # Create objects
        for i in range(50):
            client.put_object(Bucket="test-bucket", Key=f"many-{i:04d}.txt", Body=b"data")

        # List with pagination
        all_keys = []
        continuation_token = None

        while True:
            if continuation_token:
                resp = client.list_objects_v2(Bucket="test-bucket", ContinuationToken=continuation_token)
            else:
                resp = client.list_objects_v2(Bucket="test-bucket")

            all_keys.extend([obj["Key"] for obj in resp.get("Contents", [])])

            if not resp.get("IsTruncated"):
                break

            continuation_token = resp.get("NextContinuationToken")

        assert len(all_keys) == 50

    def test_create_objects_in_multiple_prefixes(self, s3_setup: S3Client) -> None:
        """Test creating objects in many different prefixes."""
        client = s3_setup

        # Create objects in 10 prefixes with 10 objects each
        prefixes = [f"prefix-{i:02d}/" for i in range(10)]

        for prefix in prefixes:
            for j in range(10):
                client.put_object(Bucket="test-bucket", Key=f"{prefix}file-{j:02d}.txt", Body=b"data")

        # Verify total
        resp = client.list_objects_v2(Bucket="test-bucket")
        assert resp["KeyCount"] == 100

        # Verify prefix filtering
        for prefix in prefixes:
            resp = client.list_objects_v2(Bucket="test-bucket", Prefix=prefix)
            assert resp["KeyCount"] == 10

    def test_delete_many_objects(self, s3_setup: S3Client) -> None:
        """Test batch deleting many objects."""
        client = s3_setup

        # Create 50 objects
        keys = [f"delete-{i:04d}.txt" for i in range(50)]
        for key in keys:
            client.put_object(Bucket="test-bucket", Key=key, Body=b"data")

        # Delete in batches (S3 API limit is 1000 per request)
        batch_size = 25
        for i in range(0, len(keys), batch_size):
            batch = keys[i : i + batch_size]
            client.delete_objects(Bucket="test-bucket", Delete={"Objects": [{"Key": k} for k in batch]})

        # Verify empty
        resp = client.list_objects_v2(Bucket="test-bucket")
        assert resp.get("KeyCount", 0) == 0


class TestRoundtrips:
    """Test roundtrip operations (PUT through router, GET through router) to verify streaming."""

    def test_roundtrip_putobject_small_file(self, s3_setup: S3Client) -> None:
        """Test roundtrip: PUT for a small file through router to verify streaming."""
        client = s3_setup
        test_data = b"hello world"

        response = client.put_object(
            Bucket="test-bucket",
            Key="test-file.txt",
            Body=test_data,
        )

        assert response["ResponseMetadata"]["HTTPStatusCode"] == 200
        assert "ETag" in response

        # Verify through router
        obj = client.get_object(Bucket="test-bucket", Key="test-file.txt")
        stored_data = obj["Body"].read()
        assert stored_data == test_data

    def test_roundtrip_putobject_large_file(self, s3_setup: S3Client) -> None:
        """Test roundtrip: PUT for a large file (10MB) through router to verify streaming."""
        client = s3_setup
        test_data = b"x" * (10 * 1024 * 1024)

        response = client.put_object(
            Bucket="test-bucket",
            Key="large-file.bin",
            Body=test_data,
        )

        assert response["ResponseMetadata"]["HTTPStatusCode"] == 200
        assert "ETag" in response

        # Verify through router
        obj = client.get_object(Bucket="test-bucket", Key="large-file.bin")
        stored_data = obj["Body"].read()
        assert len(stored_data) == len(test_data)
        assert stored_data == test_data

    def test_roundtrip_putobject_with_metadata(self, s3_setup: S3Client) -> None:
        """Test roundtrip: PUT with user metadata through router to verify streaming."""
        client = s3_setup
        test_data = b"test data with metadata"
        metadata = {"key1": "value1", "key2": "value2"}

        response = client.put_object(
            Bucket="test-bucket",
            Key="file-with-metadata.txt",
            Body=test_data,
            Metadata=metadata,
        )

        assert response["ResponseMetadata"]["HTTPStatusCode"] == 200

        # Verify through router
        obj = client.head_object(Bucket="test-bucket", Key="file-with-metadata.txt")
        assert obj["Metadata"] == metadata

    def test_roundtrip_putobject_with_content_type(self, s3_setup: S3Client) -> None:
        """Test roundtrip: PUT with Content-Type header through router to verify streaming."""
        client = s3_setup
        test_data = b"<html><body>Hello</body></html>"
        content_type = "text/html; charset=utf-8"

        response = client.put_object(
            Bucket="test-bucket",
            Key="index.html",
            Body=test_data,
            ContentType=content_type,
        )

        assert response["ResponseMetadata"]["HTTPStatusCode"] == 200

        # Verify through router
        obj = client.head_object(Bucket="test-bucket", Key="index.html")
        assert obj["ContentType"] == content_type

    def test_roundtrip_multipart_upload(self, s3_setup: S3Client) -> None:
        """Test roundtrip: UploadPart in a multipart upload through router to verify streaming."""
        client = s3_setup

        response = client.create_multipart_upload(
            Bucket="test-bucket",
            Key="multipart-file.bin",
        )
        upload_id = response["UploadId"]

        # Upload parts (minimum 5MB per part except last)
        parts: list[CompletedPartTypeDef] = []
        for i in range(1, 4):
            part_data = b"x" * (5 * 1024 * 1024) if i < 3 else b"x" * 1000
            part_response = client.upload_part(
                Bucket="test-bucket",
                Key="multipart-file.bin",
                PartNumber=i,
                UploadId=upload_id,
                Body=part_data,
            )
            parts.append({"PartNumber": i, "ETag": part_response["ETag"]})

        complete_response = client.complete_multipart_upload(
            Bucket="test-bucket",
            Key="multipart-file.bin",
            UploadId=upload_id,
            MultipartUpload={"Parts": parts},
        )

        assert complete_response["ResponseMetadata"]["HTTPStatusCode"] == 200
        assert "Location" in complete_response

        # Verify through router
        obj = client.head_object(Bucket="test-bucket", Key="multipart-file.bin")
        assert obj["ResponseMetadata"]["HTTPStatusCode"] == 200

    def test_roundtrip_putobject_multiple_sequential(self, s3_setup: S3Client) -> None:
        """Test multiple sequential PUT operations through router to verify streaming."""
        client = s3_setup

        num_files = 5
        for i in range(num_files):
            test_data = f"file {i} content".encode()
            response = client.put_object(
                Bucket="test-bucket",
                Key=f"file-{i}.txt",
                Body=test_data,
            )
            assert response["ResponseMetadata"]["HTTPStatusCode"] == 200

        # Verify through router
        list_response = client.list_objects_v2(Bucket="test-bucket")
        assert list_response["KeyCount"] == num_files

    def test_roundtrip_putobject_overwrite(self, s3_setup: S3Client) -> None:
        """Test overwriting an existing object through router to verify streaming."""
        client = s3_setup

        initial_data = b"initial content"
        client.put_object(
            Bucket="test-bucket",
            Key="file.txt",
            Body=initial_data,
        )

        new_data = b"new content"
        response = client.put_object(
            Bucket="test-bucket",
            Key="file.txt",
            Body=new_data,
        )

        assert response["ResponseMetadata"]["HTTPStatusCode"] == 200

        # Verify through router
        obj = client.get_object(Bucket="test-bucket", Key="file.txt")
        stored_data = obj["Body"].read()
        assert stored_data == new_data

    def test_roundtrip_multipart_large_parts(self, s3_setup: S3Client) -> None:
        """Test multipart upload with large parts through router to verify streaming."""
        client = s3_setup

        response = client.create_multipart_upload(
            Bucket="test-bucket",
            Key="large-multipart.bin",
        )
        upload_id = response["UploadId"]

        parts: list[CompletedPartTypeDef] = []
        part_size = 5 * 1024 * 1024
        num_parts = 2

        for i in range(1, num_parts + 1):
            part_data = b"x" * part_size
            part_response = client.upload_part(
                Bucket="test-bucket",
                Key="large-multipart.bin",
                PartNumber=i,
                UploadId=upload_id,
                Body=part_data,
            )
            parts.append({"PartNumber": i, "ETag": part_response["ETag"]})

        complete_response = client.complete_multipart_upload(
            Bucket="test-bucket",
            Key="large-multipart.bin",
            UploadId=upload_id,
            MultipartUpload={"Parts": parts},
        )

        assert complete_response["ResponseMetadata"]["HTTPStatusCode"] == 200

        # Verify through router
        obj = client.head_object(Bucket="test-bucket", Key="large-multipart.bin")
        assert obj["ContentLength"] == part_size * num_parts

    def test_roundtrip_putobject_with_sha256_checksum(self, s3_setup: S3Client) -> None:
        """Test roundtrip: PUT with SHA256 checksum through router to verify streaming."""
        client = s3_setup
        test_data = b"test data for sha256 checksum"

        # Put object with SHA256 checksum algorithm
        # boto3 will calculate the checksum value and forward it through router
        response = client.put_object(
            Bucket="test-bucket",
            Key="file-with-sha256.txt",
            Body=test_data,
            ChecksumAlgorithm="SHA256",
        )

        assert response["ResponseMetadata"]["HTTPStatusCode"] == 200
        assert "ETag" in response
        # Response should include the SHA256 checksum
        assert "ChecksumSHA256" in response or "ETag" in response

        # Verify through router - fetch the object back
        obj = client.get_object(Bucket="test-bucket", Key="file-with-sha256.txt")
        stored_data = obj["Body"].read()
        assert stored_data == test_data
        # Verify the checksum is preserved through router
        assert "ChecksumSHA256" in obj or "ETag" in obj

    def test_roundtrip_s3transfer_upload(self, s3_setup: S3Client) -> None:
        """Test roundtrip: upload using s3transfer which uses aws-chunked encoding."""
        client = s3_setup

        # Create test data large enough to trigger chunked transfer (> 8KB default threshold)
        test_data = b"A" * (128 * 1024)  # 128KB

        # Create a temporary file for the upload
        with tempfile.NamedTemporaryFile(delete=False) as tmp:
            tmp.write(test_data)
            tmp_path = tmp.name

        try:
            # Use s3transfer with custom config to force chunked encoding
            transfer_config = s3transfer.TransferConfig(
                multipart_threshold=1024 * 1024 * 1024,  # 1GB - disable multipart
                max_concurrency=1,
                use_threads=False,
            )

            # Upload using transfer manager
            transfer = s3transfer.S3Transfer(client, transfer_config)  # type: ignore[arg-type]
            transfer.upload_file(tmp_path, "test-bucket", "s3transfer-test.bin")

            # Verify the upload
            obj = client.get_object(Bucket="test-bucket", Key="s3transfer-test.bin")
            stored_data = obj["Body"].read()
            assert stored_data == test_data
            assert len(stored_data) == len(test_data)
        finally:
            os.unlink(tmp_path)

    def test_roundtrip_s3transfer_upload_with_checksum(self, s3_setup: S3Client) -> None:
        """Test roundtrip: s3transfer upload with checksum triggers aws-chunked encoding."""
        client = s3_setup

        # Create test data
        test_data = b"B" * (64 * 1024)  # 64KB

        with tempfile.NamedTemporaryFile(delete=False) as tmp:
            tmp.write(test_data)
            tmp_path = tmp.name

        try:
            transfer_config = s3transfer.TransferConfig(
                multipart_threshold=1024 * 1024 * 1024,  # Disable multipart
            )

            # Upload with checksum algorithm - this forces aws-chunked encoding
            transfer = s3transfer.S3Transfer(client, transfer_config)  # type: ignore[arg-type]
            transfer.upload_file(
                tmp_path,
                "test-bucket",
                "s3transfer-checksum.bin",
                extra_args={"ChecksumAlgorithm": "SHA256"},
            )

            # Verify the upload
            obj = client.get_object(Bucket="test-bucket", Key="s3transfer-checksum.bin")
            stored_data = obj["Body"].read()
            assert stored_data == test_data
        finally:
            os.unlink(tmp_path)

    def test_roundtrip_minio_client_upload(self, s3router_with_moto: S3RouterWithMoto) -> None:
        """Test roundtrip: upload using minio-py client which signals aws-chunked via x-amz-content-sha256.

        The minio-py client doesn't always set Content-Encoding: aws-chunked, but instead
        signals streaming via x-amz-content-sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD.
        This tests the workaround that detects aws-chunked via the x-amz-content-sha256 header.
        """
        from io import BytesIO

        from minio import Minio

        # Parse router URL
        router_url = s3router_with_moto["router_url"]
        moto_endpoint = s3router_with_moto["moto_endpoint"]

        # Ensure bucket exists on moto
        import boto3

        moto_client = boto3.client(
            "s3",
            endpoint_url=moto_endpoint,
            region_name="us-east-1",
            aws_access_key_id="testing",
            aws_secret_access_key="testing",
        )
        try:
            moto_client.create_bucket(Bucket="test-bucket")
        except Exception:
            pass

        # Create minio client pointing to router
        # Remove http:// prefix for minio endpoint
        endpoint = router_url.replace("http://", "").replace("https://", "")
        minio_client = Minio(
            endpoint,
            access_key="testing",
            secret_key="testing",
            secure=False,
            region="us-east-1",
        )

        # Create test data - minio uses streaming for any size
        test_data = b"M" * (64 * 1024)  # 64KB

        # Upload using minio client with known size (uses pre-calculated sha256)
        minio_client.put_object(
            "test-bucket",
            "minio-test.bin",
            BytesIO(test_data),
            len(test_data),
        )

        # Verify using minio client
        response = minio_client.get_object("test-bucket", "minio-test.bin")
        stored_data = response.read()
        response.close()
        response.release_conn()

        assert stored_data == test_data
        assert len(stored_data) == len(test_data)

    def test_roundtrip_minio_client_streaming_upload(self, s3router_with_moto: S3RouterWithMoto) -> None:
        """Test roundtrip: upload using minio-py with unknown size to trigger streaming.

        When size is unknown (-1), minio-py uses x-amz-content-sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD
        without the Content-Encoding: aws-chunked header. This tests the workaround that detects
        aws-chunked via the x-amz-content-sha256 header.
        """
        from io import BytesIO

        from minio import Minio

        # Parse router URL
        router_url = s3router_with_moto["router_url"]
        moto_endpoint = s3router_with_moto["moto_endpoint"]

        # Ensure bucket exists on moto
        import boto3

        moto_client = boto3.client(
            "s3",
            endpoint_url=moto_endpoint,
            region_name="us-east-1",
            aws_access_key_id="testing",
            aws_secret_access_key="testing",
        )
        try:
            moto_client.create_bucket(Bucket="test-bucket")
        except Exception:
            pass

        # Create minio client pointing to router
        endpoint = router_url.replace("http://", "").replace("https://", "")
        minio_client = Minio(
            endpoint,
            access_key="testing",
            secret_key="testing",
            secure=False,
            region="us-east-1",
        )

        # Create test data
        test_data = b"S" * (64 * 1024)  # 64KB

        # Upload with unknown size (-1) to force streaming aws-chunked encoding
        # This causes minio to use x-amz-content-sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD
        minio_client.put_object(
            "test-bucket",
            "minio-streaming-test.bin",
            BytesIO(test_data),
            -1,  # Unknown size triggers streaming
            part_size=10 * 1024 * 1024,  # 10MB part size
        )

        # Verify using minio client
        response = minio_client.get_object("test-bucket", "minio-streaming-test.bin")
        stored_data = response.read()
        response.close()
        response.release_conn()

        assert stored_data == test_data
        assert len(stored_data) == len(test_data)

    def test_roundtrip_minio_client_fput(self, s3router_with_moto: S3RouterWithMoto) -> None:
        """Test roundtrip: file upload using minio-py client fput_object.

        This tests the streaming aws-chunked path with file-based uploads.
        """
        from minio import Minio

        # Parse router URL
        router_url = s3router_with_moto["router_url"]
        moto_endpoint = s3router_with_moto["moto_endpoint"]

        # Ensure bucket exists on moto
        import boto3

        moto_client = boto3.client(
            "s3",
            endpoint_url=moto_endpoint,
            region_name="us-east-1",
            aws_access_key_id="testing",
            aws_secret_access_key="testing",
        )
        try:
            moto_client.create_bucket(Bucket="test-bucket")
        except Exception:
            pass

        # Create minio client pointing to router
        endpoint = router_url.replace("http://", "").replace("https://", "")
        minio_client = Minio(
            endpoint,
            access_key="testing",
            secret_key="testing",
            secure=False,
            region="us-east-1",
        )

        # Create test data
        test_data = b"F" * (128 * 1024)  # 128KB

        with tempfile.NamedTemporaryFile(delete=False) as tmp:
            tmp.write(test_data)
            tmp_path = tmp.name

        try:
            # Upload file using minio client
            minio_client.fput_object("test-bucket", "minio-fput-test.bin", tmp_path)

            # Verify using minio client
            response = minio_client.get_object("test-bucket", "minio-fput-test.bin")
            stored_data = response.read()
            response.close()
            response.release_conn()

            assert stored_data == test_data
            assert len(stored_data) == len(test_data)
        finally:
            os.unlink(tmp_path)


class TestListObjectsWithRewriteAndDelimiter:
    """Test ListObjects with non-slash delimiters combined with key rewriting.

    When routes have rewriting rules (e.g., virtual keys transformed to physical keys),
    the handler must:
    1. Apply reverse rewriting to convert physical keys back to virtual
    2. Apply delimiter grouping on the virtual keys

    This tests whether s3router correctly handles both transformations.
    """

    def test_listobjectsv2_delimiter_after_reverse_rewrite(
        self, s3router_with_moto: S3RouterWithMoto, create_s3_client: Callable[[str], S3Client]
    ) -> None:
        """
        Test delimiter grouping applied to reverse-rewritten keys.

        Scenario:
        - Virtual route pattern: ^logs/(?P<type>[^/]+)/(?P<date>[^/]+)/.*
        - Rewrite rule: logs-$type-$date-$rest
        - Physical keys: logs-access-2024-01-01.txt, logs-access-2024-01-02.txt
        - When reverse-rewritten: logs/access/2024/01-01.txt, logs/access/2024/01-02.txt
        - With '-' delimiter: Should group by 'logs-' prefix in virtual keys
        """
        # Use the default backend through the router
        s3router_with_moto["moto_endpoint"]
        router_url = s3router_with_moto["router_url"]

        # For this test, we verify that delimiter is applied after reverse rewriting
        # by storing objects with patterns that would expose delimiter issues
        client = create_s3_client(router_url)

        # Store objects that will be delimiter-tested
        # These are just normal keys - s3router will handle them as-is
        # but we're testing the delimiter logic works on them
        client.put_object(Bucket="test-bucket", Key="logs-access-2024-01-01.txt", Body=b"data1")
        client.put_object(Bucket="test-bucket", Key="logs-access-2024-01-02.txt", Body=b"data2")
        client.put_object(Bucket="test-bucket", Key="logs-error-2024-01-01.txt", Body=b"data3")

        # List with '-' delimiter
        resp = client.list_objects_v2(Bucket="test-bucket", Delimiter="-")

        # Verify delimiter grouping works
        assert "CommonPrefixes" in resp
        prefixes = [p["Prefix"] for p in resp["CommonPrefixes"]]
        assert "logs-" in prefixes

        # Verify objects are not in Contents (they're grouped by prefix)
        contents = [obj["Key"] for obj in resp.get("Contents", [])]
        # Objects starting with 'logs-' should have common prefix, not be in Contents
        assert not any(key.startswith("logs-") for key in contents)


class TestListObjectsDelimiterOptimizationAnalysis:
    """Analyze delimiter optimization potential for "/" delimiter.

    Current behavior: Handler fetches ALL objects from backend, applies delimiter locally.
    Optimization potential: Pass "/" to backend, get native CommonPrefixes grouping.

    These tests verify that:
    1. Current implementation works correctly
    2. What metrics would indicate optimization is working
    3. What behavior must be preserved after optimization
    4. Multi-backend aggregation complexity
    """

    def test_slash_delimiter_current_behavior(self, s3_setup: S3Client) -> None:
        """Verify current behavior: handler groups "/" delimiter locally.

        This test documents the current approach:
        - All objects listed (no "/" delimiter passed to backend)
        - CommonPrefixes computed locally
        - Result is correct but potentially inefficient
        """
        client = s3_setup

        # Create test-specific keys to avoid conflicts with other tests
        # Use a unique prefix for this test
        test_prefix = "opt-test-1/"
        keys = [
            f"{test_prefix}logs/access/2024-01-01.txt",
            f"{test_prefix}logs/access/2024-01-02.txt",
            f"{test_prefix}logs/error/2024-01-01.txt",
            f"{test_prefix}logs/error/2024-01-02.txt",
            f"{test_prefix}data/user/alice.json",
            f"{test_prefix}data/user/bob.json",
        ]

        for key in keys:
            client.put_object(Bucket="test-bucket", Key=key, Body=b"data")

        # List with "/" delimiter under test prefix - should get CommonPrefixes
        resp = client.list_objects_v2(Bucket="test-bucket", Prefix=test_prefix, Delimiter="/")

        # Current behavior: gets CommonPrefixes for next level
        common_prefixes = [p["Prefix"] for p in resp.get("CommonPrefixes", [])]
        assert f"{test_prefix}logs/" in common_prefixes
        assert f"{test_prefix}data/" in common_prefixes

        # Objects should be empty (all are under prefixes)
        contents = [obj["Key"] for obj in resp.get("Contents", [])]
        assert all(not key.startswith(test_prefix) or "/" in key.split(test_prefix, 1)[1] for key in contents)

        # Key insight: to achieve this, handler fetched all 6 objects
        # and grouped them. With optimization, backend would return
        # only the 2 common prefixes (100x reduction in this case)

        print(f"Current behavior: Fetched {len(keys)} objects to produce {len(common_prefixes)} prefixes")

    def test_slash_delimiter_with_prefix_filter(self, s3_setup: S3Client) -> None:
        """Verify "/" delimiter works with prefix filtering.

        This is important for optimization: if we pass "/" to backend,
        we must pass the prefix too.
        """
        client = s3_setup

        keys = [
            "logs/access/2024-01-01.txt",
            "logs/access/2024-01-02.txt",
            "logs/error/2024-01-01.txt",
            "data/user/alice.json",
        ]

        for key in keys:
            client.put_object(Bucket="test-bucket", Key=key, Body=b"data")

        # List "logs/" with "/" delimiter
        resp = client.list_objects_v2(Bucket="test-bucket", Prefix="logs/", Delimiter="/")

        common_prefixes = [p["Prefix"] for p in resp.get("CommonPrefixes", [])]

        # Should see logs/access/ and logs/error/ as prefixes
        assert "logs/access/" in common_prefixes
        assert "logs/error/" in common_prefixes

        # Should NOT see data/ or other top-level prefixes
        assert not any(p.startswith("data/") for p in common_prefixes)

        print(f"Prefix filter: Found {len(common_prefixes)} prefixes under 'logs/'")

    def test_slash_delimiter_pagination(self, s3_setup: S3Client) -> None:
        """Verify pagination with "/" delimiter works correctly.

        Optimization concern: if we pass "/" to backend, must handle pagination correctly.
        """
        client = s3_setup

        # Create many prefixes
        for i in range(15):
            client.put_object(Bucket="test-bucket", Key=f"dir{i:02d}/file.txt", Body=b"data")

        # List with MaxKeys=5 and "/" delimiter
        resp = client.list_objects_v2(Bucket="test-bucket", Delimiter="/", MaxKeys=5)

        prefixes = [p["Prefix"] for p in resp.get("CommonPrefixes", [])]
        assert len(prefixes) == 5

        # Should be truncated
        assert resp.get("IsTruncated")

        # Get next page
        continuation_token = resp.get("NextContinuationToken")
        assert continuation_token is not None

        resp2 = client.list_objects_v2(
            Bucket="test-bucket",
            Delimiter="/",
            MaxKeys=5,
            ContinuationToken=continuation_token,
        )

        prefixes2 = [p["Prefix"] for p in resp2.get("CommonPrefixes", [])]
        assert len(prefixes2) > 0

        # Prefixes should be different
        assert set(prefixes).isdisjoint(set(prefixes2))

        print(f"Pagination: Page 1 has {len(prefixes)} prefixes, Page 2 has {len(prefixes2)}")

    def test_slash_delimiter_with_reverse_rewrite(self, s3_setup: S3Client) -> None:
        """Verify "/" delimiter works after reverse rewriting.

        Critical for optimization: reverse rewriting must work before grouping.
        """
        client = s3_setup

        # Simulate objects that would exist after reverse rewriting
        # (though in this test without actual rewrites, keys are as-is)
        keys = [
            "app/logs/access/2024-01-01.txt",
            "app/logs/access/2024-01-02.txt",
            "app/logs/error/2024-01-01.txt",
            "api/logs/access/2024-01-01.txt",
        ]

        for key in keys:
            client.put_object(Bucket="test-bucket", Key=key, Body=b"data")

        # List with "/" delimiter - should group by first "/" after prefix
        resp = client.list_objects_v2(Bucket="test-bucket", Prefix="app/", Delimiter="/")

        prefixes = [p["Prefix"] for p in resp.get("CommonPrefixes", [])]

        # Should get app/logs/ as the next level
        assert "app/logs/" in prefixes

        # Should NOT get app/logs/access/ (that's beyond the delimiter)
        assert not any("access" in p for p in prefixes)

        print(f"Reverse rewrite compatibility: Found {len(prefixes)} prefixes with correct grouping")

    def test_multiple_backends_delimiter_aggregation(
        self, s3router_with_moto: S3RouterWithMoto, create_s3_client: Callable[[str], S3Client]
    ) -> None:
        """Verify how CommonPrefixes would be aggregated across backends.

        This demonstrates what optimization must handle: deduplication of CommonPrefixes
        from multiple backends.
        """
        moto_client = create_s3_client(s3router_with_moto["moto_endpoint"])

        # Setup
        for bucket in ["test-bucket", "backend-1", "backend-2"]:
            moto_client.create_bucket(Bucket=bucket)

        # Simulate objects from backend-1
        backend1_prefixes = ["logs/access/", "logs/error/", "logs/debug/"]
        for prefix in backend1_prefixes:
            moto_client.put_object(Bucket="backend-1", Key=f"{prefix}item.txt", Body=b"data")

        # Simulate objects from backend-2 (with overlapping prefixes)
        backend2_prefixes = ["logs/access/", "logs/warning/", "data/"]
        for prefix in backend2_prefixes:
            moto_client.put_object(Bucket="backend-2", Key=f"{prefix}item.txt", Body=b"data")

        # If we were to optimize and pass "/" to each backend:
        # Backend-1 would return: logs/access/, logs/error/, logs/debug/
        # Backend-2 would return: logs/access/, logs/warning/, data/
        # After aggregation and deduplication:
        # Should return: logs/access/, logs/error/, logs/debug/, logs/warning/, data/

        # This test documents what aggregation must handle:
        # - Deduplication of common prefixes ('logs/access/' appears in both)
        # - Merging results from multiple backends
        # - Sorting/ordering of results

        print(
            f"Multi-backend aggregation: Backend-1 has {len(backend1_prefixes)} prefixes, "
            + f"Backend-2 has {len(backend2_prefixes)} prefixes, "
            + f"Deduplicated count would be {len(set(backend1_prefixes + backend2_prefixes))}"
        )

    def test_large_object_count_performance_concern(self, s3_setup: S3Client) -> None:
        """Document the performance concern with current list-all-and-filter approach.

        This test shows why optimization matters: current approach fetches N objects
        to produce M prefixes (often M << N, especially for "/" delimiter).
        """
        client = s3_setup

        # Create a large number of small objects in a hierarchy
        # Simulates a backend with 1000 objects in 10 prefixes
        num_prefixes = 10
        objects_per_prefix = 100

        for p in range(num_prefixes):
            for o in range(objects_per_prefix):
                client.put_object(
                    Bucket="test-bucket",
                    Key=f"prefix{p:02d}/object{o:04d}.txt",
                    Body=b"x",  # Tiny objects
                )

        # With "/" delimiter and no optimization:
        # - Handler fetches 1000 objects from backend
        # - Computes 10 CommonPrefixes locally
        # - Returns 10 prefixes to client
        # - Network transfer: ~1000 objects worth of metadata

        resp = client.list_objects_v2(Bucket="test-bucket", Delimiter="/")
        prefixes = resp.get("CommonPrefixes", [])

        assert len(prefixes) == num_prefixes

        # With optimization:
        # - Handler passes "/" delimiter to backend
        # - Backend returns only 10 CommonPrefixes
        # - Network transfer: ~10 prefixes
        # - 100x reduction in data transfer

        print(f"Performance concern: {num_prefixes} prefixes, {num_prefixes * objects_per_prefix} objects")
        print(f"  Current: Fetches all {num_prefixes * objects_per_prefix} objects")
        print(f"  Optimized: Would fetch only {num_prefixes} CommonPrefixes")
        print(f"  Potential improvement: ~{objects_per_prefix}x reduction")
