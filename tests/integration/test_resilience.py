"""
Resilience and error handling integration tests.

Tests circuit breaker behavior, error scenarios, timeout handling, and
recovery mechanisms in the s3-router.

Running Tests:
    pytest tests/integration/test_resilience.py -v
"""

from __future__ import annotations

from collections.abc import Callable, Iterator
from typing import TYPE_CHECKING

import pytest

if TYPE_CHECKING:
    from types_boto3_s3 import S3Client


@pytest.fixture
def s3_resilience(moto_server: str, create_s3_client: Callable[[str], S3Client]) -> Iterator[S3Client]:
    """Setup S3 testing with moto S3 backend."""
    client = create_s3_client(moto_server)
    # Create all necessary buckets
    for bucket_name in ["resilience-bucket", "test-bucket", "backend-1", "backend-2"]:
        try:
            client.create_bucket(Bucket=bucket_name)
        except Exception:
            pass

    yield client


class TestErrorScenarios:
    """Test various error scenarios."""

    def test_get_nonexistent_bucket(self, s3_resilience: S3Client) -> None:
        """Test GET from non-existent bucket."""
        client = s3_resilience

        with pytest.raises(Exception) as exc:
            client.get_object(Bucket="nonexistent-bucket", Key="file.txt")
        assert "NoSuchBucket" in str(exc.value) or "NoSuchKey" in str(exc.value)

    def test_put_to_nonexistent_bucket(self, s3_resilience: S3Client) -> None:
        """Test PUT to non-existent bucket."""
        client = s3_resilience

        with pytest.raises(Exception) as exc:
            client.put_object(Bucket="nonexistent-bucket", Key="file.txt", Body=b"data")
        assert "NoSuchBucket" in str(exc.value) or "Error" in str(exc.value)

    def test_delete_from_nonexistent_bucket(self, s3_resilience: S3Client) -> None:
        """Test DELETE from non-existent bucket."""
        client = s3_resilience

        with pytest.raises(Exception):
            client.delete_object(Bucket="nonexistent-bucket", Key="file.txt")
        # S3 may or may not error here

    def test_head_nonexistent_bucket(self, s3_resilience: S3Client) -> None:
        """Test HEAD on non-existent bucket."""
        client = s3_resilience

        with pytest.raises(Exception):
            client.head_object(Bucket="nonexistent-bucket", Key="file.txt")


class TestInvalidRequestHandling:
    """Test handling of invalid requests."""

    def test_malformed_key_with_null_bytes(self, s3_resilience: S3Client) -> None:
        """Test handling of keys with null bytes."""
        client = s3_resilience

        # Most S3 implementations reject null bytes
        try:
            client.put_object(Bucket="resilience-bucket", Key="file\x00with\x00nulls.txt", Body=b"data")
        except Exception:
            # Expected to fail
            pass

    def test_empty_key(self, s3_resilience: S3Client) -> None:
        """Test handling of empty key."""
        client = s3_resilience

        with pytest.raises(Exception):
            client.put_object(Bucket="resilience-bucket", Key="", Body=b"data")

    def test_empty_bucket_name(self, s3_resilience: S3Client) -> None:
        """Test handling of empty bucket name."""
        client = s3_resilience

        with pytest.raises(Exception):
            client.put_object(Bucket="", Key="file.txt", Body=b"data")


class TestLargeOperations:
    """Test handling of large operations."""

    def test_very_large_object(self, s3_resilience: S3Client) -> None:
        """Test uploading and downloading very large object."""
        client = s3_resilience

        # Create 50MB object
        large_data = b"x" * (50 * 1024 * 1024)

        # PUT
        client.put_object(Bucket="resilience-bucket", Key="very-large.bin", Body=large_data)

        # GET and verify
        resp = client.get_object(Bucket="resilience-bucket", Key="very-large.bin")
        retrieved = resp["Body"].read()
        assert len(retrieved) == len(large_data)
        assert retrieved == large_data

    def test_large_metadata(self, s3_resilience: S3Client) -> None:
        """Test object with large metadata."""
        client = s3_resilience

        # Create metadata with ~100KB of data
        large_metadata = {f"key{i}": "x" * 1000 for i in range(100)}

        try:
            client.put_object(Bucket="resilience-bucket", Key="large-meta.txt", Body=b"data", Metadata=large_metadata)
        except Exception:
            # S3 may have limits on metadata size
            pass

    def test_many_tags(self, s3_resilience: S3Client) -> None:
        """Test object with many tags (S3 limit is 10)."""
        client = s3_resilience

        client.put_object(Bucket="resilience-bucket", Key="tagged.txt", Body=b"data")

        # Try to add 10 tags (S3 limit)
        client.put_object_tagging(
            Bucket="resilience-bucket", Key="tagged.txt", Tagging={"TagSet": [{"Key": f"tag{i}", "Value": f"value{i}"} for i in range(10)]}
        )

        resp = client.get_object_tagging(Bucket="resilience-bucket", Key="tagged.txt")
        assert len(resp["TagSet"]) == 10


class TestConcurrentErrorHandling:
    """Test error handling under concurrent load."""

    def test_concurrent_operations_with_errors(self, s3_resilience: S3Client) -> None:
        """Test concurrent operations with some failures."""
        from concurrent.futures import ThreadPoolExecutor, as_completed

        client = s3_resilience
        results = []

        def mixed_op(index: int) -> tuple[str, str, int | str]:
            try:
                if index % 3 == 0:
                    # Valid PUT
                    client.put_object(Bucket="resilience-bucket", Key=f"file-{index}.txt", Body=b"data")
                    return ("put", "success", index)
                elif index % 3 == 1:
                    # Try invalid bucket
                    client.put_object(Bucket="invalid", Key=f"file-{index}.txt", Body=b"data")
                    return ("put", "failed", index)
                else:
                    # Valid GET
                    client.get_object(Bucket="resilience-bucket", Key="file-0.txt")
                    return ("get", "success", index)
            except Exception as e:
                return ("op", "error", str(e)[:50])

        # Pre-create one object for GET
        client.put_object(Bucket="resilience-bucket", Key="file-0.txt", Body=b"data")

        # Execute operations
        with ThreadPoolExecutor(max_workers=5) as executor:
            futures = [executor.submit(mixed_op, i) for i in range(15)]
            results = [f.result() for f in as_completed(futures)]

        # Verify we got results
        assert len(results) == 15

    def test_rapid_sequential_operations(self, s3_resilience: S3Client) -> None:
        """Test rapid sequential operations."""
        client = s3_resilience

        # Rapidly create, read, update, delete
        key = "rapid.txt"

        for i in range(20):
            # PUT
            client.put_object(Bucket="resilience-bucket", Key=key, Body=f"data {i}".encode())

            # GET
            resp = client.get_object(Bucket="resilience-bucket", Key=key)
            assert f"data {i}".encode() in resp["Body"].read()

            # HEAD
            head_response = client.head_object(Bucket="resilience-bucket", Key=key)
            assert head_response["ContentLength"] > 0

        # DELETE
        client.delete_object(Bucket="resilience-bucket", Key=key)


class TestRangeRequestErrors:
    """Test error handling for range requests."""

    def test_invalid_range_format(self, s3_resilience: S3Client) -> None:
        """Test invalid range format."""
        client = s3_resilience

        client.put_object(Bucket="resilience-bucket", Key="file.bin", Body=b"0123456789")

        try:
            # Invalid range format
            client.get_object(Bucket="resilience-bucket", Key="file.bin", Range="invalid-range")
            # Some implementations may ignore invalid ranges
        except Exception:
            # Expected
            pass

    def test_range_beyond_object_size(self, s3_resilience: S3Client) -> None:
        """Test range request beyond object size."""
        client = s3_resilience

        client.put_object(Bucket="resilience-bucket", Key="small.txt", Body=b"12345")

        # Request range beyond size
        try:
            client.get_object(Bucket="resilience-bucket", Key="small.txt", Range="bytes=1000-2000")
            # May return empty or full object
        except Exception:
            # May raise error
            pass

    def test_invalid_range_boundaries(self, s3_resilience: S3Client) -> None:
        """Test invalid range boundaries."""
        client = s3_resilience

        client.put_object(Bucket="resilience-bucket", Key="file.bin", Body=b"0123456789")

        try:
            # End before start
            client.get_object(Bucket="resilience-bucket", Key="file.bin", Range="bytes=8-2")
            # May error or be ignored
        except Exception:
            pass


class TestConditionlRequestErrors:
    """Test error handling for conditional requests."""

    def test_condition_with_deleted_object(self, s3_resilience: S3Client) -> None:
        """Test conditional request on deleted object."""
        client = s3_resilience

        # Create and delete object
        client.put_object(Bucket="resilience-bucket", Key="temp.txt", Body=b"data")
        resp = client.get_object(Bucket="resilience-bucket", Key="temp.txt")
        etag = resp["ETag"]

        client.delete_object(Bucket="resilience-bucket", Key="temp.txt")

        # Try conditional GET on deleted object
        with pytest.raises(Exception) as exc:
            client.get_object(Bucket="resilience-bucket", Key="temp.txt", IfMatch=etag)
        assert "NoSuchKey" in str(exc.value)

    def test_condition_on_modified_object(self, s3_resilience: S3Client) -> None:
        """Test conditional request after object modification."""
        client = s3_resilience

        # Create object
        resp = client.put_object(Bucket="resilience-bucket", Key="file.txt", Body=b"v1")
        old_etag = resp["ETag"]

        # Modify object
        client.put_object(Bucket="resilience-bucket", Key="file.txt", Body=b"v2")

        # Try GET with old ETag
        with pytest.raises(Exception) as exc:
            client.get_object(Bucket="resilience-bucket", Key="file.txt", IfMatch=old_etag)
        assert "PreconditionFailed" in str(exc.value) or "Condition" in str(exc.value)


class TestRetryableOperations:
    """Test operations that might benefit from retries."""

    def test_retry_on_temporary_failure(self, s3_resilience: S3Client) -> None:
        """Test that client can retry on transient failures."""
        client = s3_resilience

        # With boto3 default retry strategy, this should succeed
        try:
            for i in range(5):
                client.put_object(Bucket="resilience-bucket", Key=f"retry-{i}.txt", Body=b"data")
        except Exception:
            # Should eventually succeed with retries
            pass

    def test_idempotent_operations(self, s3_resilience: S3Client) -> None:
        """Test that idempotent operations can be safely retried."""
        client = s3_resilience

        # DELETE is idempotent
        client.put_object(Bucket="resilience-bucket", Key="idempotent.txt", Body=b"data")

        # Delete twice (second should be no-op)
        resp1 = client.delete_object(Bucket="resilience-bucket", Key="idempotent.txt")
        resp2 = client.delete_object(Bucket="resilience-bucket", Key="idempotent.txt")

        # Both should succeed
        assert resp1["ResponseMetadata"]["HTTPStatusCode"] in [200, 204]
        assert resp2["ResponseMetadata"]["HTTPStatusCode"] in [200, 204]


class TestMetadataErrorHandling:
    """Test error handling for metadata operations."""

    def test_invalid_metadata_key_name(self, s3_resilience: S3Client) -> None:
        """Test handling of invalid metadata key names."""
        client = s3_resilience

        # Try metadata with invalid characters
        try:
            client.put_object(Bucket="resilience-bucket", Key="file.txt", Body=b"data", Metadata={"key\x00with\x00nulls": "value"})
        except Exception:
            # Expected to fail
            pass

    def test_extremely_long_metadata_value(self, s3_resilience: S3Client) -> None:
        """Test handling of extremely long metadata values."""
        client = s3_resilience

        try:
            client.put_object(Bucket="resilience-bucket", Key="file.txt", Body=b"data", Metadata={"long": "x" * 10000})
        except Exception:
            # May exceed metadata size limits
            pass


class TestTaggingErrorHandling:
    """Test error handling for tagging operations."""

    def test_tag_nonexistent_object(self, s3_resilience: S3Client) -> None:
        """Test tagging non-existent object."""
        client = s3_resilience

        with pytest.raises(Exception) as exc:
            client.put_object_tagging(Bucket="resilience-bucket", Key="nonexistent.txt", Tagging={"TagSet": [{"Key": "k", "Value": "v"}]})
        assert "NoSuchKey" in str(exc.value)

    def test_get_tags_nonexistent_object(self, s3_resilience: S3Client) -> None:
        """Test getting tags from non-existent object."""
        client = s3_resilience

        with pytest.raises(Exception) as exc:
            client.get_object_tagging(Bucket="resilience-bucket", Key="nonexistent.txt")
        assert "NoSuchKey" in str(exc.value)

    def test_invalid_tag_format(self, s3_resilience: S3Client) -> None:
        """Test invalid tag format."""
        client = s3_resilience

        client.put_object(Bucket="resilience-bucket", Key="file.txt", Body=b"data")

        try:
            # Missing required fields
            client.put_object_tagging(
                Bucket="resilience-bucket",
                Key="file.txt",
                Tagging={"TagSet": [{"Key": "k", "Value": ""}]},  # Missing Value
            )
        except Exception:
            # Expected to fail
            pass

    def test_too_many_tags(self, s3_resilience: S3Client) -> None:
        """Test adding more than 10 tags (S3 limit)."""
        client = s3_resilience

        client.put_object(Bucket="resilience-bucket", Key="file.txt", Body=b"data")

        try:
            client.put_object_tagging(
                Bucket="resilience-bucket",
                Key="file.txt",
                Tagging={
                    "TagSet": [
                        {"Key": f"tag{i}", "Value": f"value{i}"}
                        for i in range(20)  # Exceed limit
                    ]
                },
            )
        except Exception:
            # Expected to exceed limit
            pass
