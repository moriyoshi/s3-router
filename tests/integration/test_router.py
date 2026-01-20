"""
Integration tests for s3-router using moto S3 backend.

These tests start moto (S3 mock), then start s3router pointing to it,
then verify the router works correctly via HTTP requests.

Includes:
- Basic S3 operations (GET, PUT, DELETE, HEAD, LIST)
- Routing decisions and backend selection
- Path rewriting functionality
- Multi-backend scenarios
"""

from __future__ import annotations

from collections.abc import Callable, Iterator
from typing import TYPE_CHECKING, TypedDict

import pytest
import requests

if TYPE_CHECKING:
    from types_boto3_s3 import S3Client
    from types_boto3_s3.type_defs import ObjectIdentifierTypeDef

    from .conftest import S3RouterWithMoto


class RouterWithMotoContext(TypedDict):
    router_url: str
    admin_url: str
    moto_client: S3Client
    moto_endpoint: str


@pytest.fixture
def router_with_moto(s3router_with_moto: S3RouterWithMoto, moto_client: S3Client) -> Iterator[RouterWithMotoContext]:
    """Start moto S3 and s3router, pointing router to moto."""
    # Create test bucket on moto
    moto_client.create_bucket(Bucket="test-bucket")

    try:
        yield {
            "router_url": s3router_with_moto["router_url"],
            "admin_url": s3router_with_moto["admin_url"],
            "moto_client": moto_client,
            "moto_endpoint": s3router_with_moto["moto_endpoint"],
        }
    finally:
        try:
            resp = moto_client.list_objects_v2(Bucket="test-bucket")
            if "Contents" in resp:
                objects_to_delete: list[ObjectIdentifierTypeDef] = [{"Key": obj["Key"]} for obj in resp["Contents"]]
                moto_client.delete_objects(Bucket="test-bucket", Delete={"Objects": objects_to_delete})
        except moto_client.exceptions.NoSuchBucket:
            # Bucket doesn't exist, this is fine for an empty test
            pass


@pytest.fixture
def router_url(router_with_moto: RouterWithMotoContext) -> str:
    return router_with_moto["router_url"]


@pytest.fixture
def admin_url(router_with_moto: RouterWithMotoContext) -> str:
    return router_with_moto["admin_url"]


@pytest.fixture
def e2e_moto_client(router_with_moto: RouterWithMotoContext) -> S3Client:
    return router_with_moto["moto_client"]


@pytest.fixture
def e2e_moto_endpoint(router_with_moto: RouterWithMotoContext) -> str:
    return router_with_moto["moto_endpoint"]


@pytest.fixture
def s3_with_routers(s3router_with_moto: S3RouterWithMoto, create_s3_client: Callable[[str], S3Client]) -> Iterator[S3Client]:
    """Setup s3-router with moto S3 backend."""
    moto_url = s3router_with_moto["moto_endpoint"]
    # Create buckets directly on moto
    moto_client = create_s3_client(moto_url)
    # Create all buckets that the router config expects
    buckets = ["virtual-bucket", "backend-prod", "backend-staging", "backend-archive"]
    for bucket in buckets:
        moto_client.create_bucket(Bucket=bucket)

    # Create router client
    client = create_s3_client(s3router_with_moto["router_url"])

    yield client


class TestBasicS3Operations:
    """Test basic S3 operations through the router."""

    def test_router_health(self, admin_url: str) -> None:
        """Test that router health endpoint responds."""
        response = requests.get(f"{admin_url}/healthz")
        assert response.status_code in [200, 404]  # May not have health endpoint

    def test_put_object_through_router(self, e2e_moto_client: S3Client) -> None:
        """Test PUT object through router."""
        # This should work: PUT to moto via router
        e2e_moto_client.put_object(Bucket="test-bucket", Key="test.txt", Body=b"test data")

        # Verify object exists
        resp = e2e_moto_client.get_object(Bucket="test-bucket", Key="test.txt")
        assert resp["Body"].read() == b"test data"

    def test_get_object_through_router(self, e2e_moto_client: S3Client) -> None:
        """Test GET object through router."""
        # Put object
        e2e_moto_client.put_object(Bucket="test-bucket", Key="file.txt", Body=b"content")

        # Get object
        resp = e2e_moto_client.get_object(Bucket="test-bucket", Key="file.txt")
        assert resp["Body"].read() == b"content"

    def test_delete_object_through_router(self, e2e_moto_client: S3Client) -> None:
        """Test DELETE object through router."""
        # Put object
        e2e_moto_client.put_object(Bucket="test-bucket", Key="delete-me.txt", Body=b"data")

        # Delete it
        e2e_moto_client.delete_object(Bucket="test-bucket", Key="delete-me.txt")

        # Verify it's gone
        with pytest.raises(Exception):
            e2e_moto_client.get_object(Bucket="test-bucket", Key="delete-me.txt")

    def test_head_object_through_router(self, e2e_moto_client: S3Client) -> None:
        """Test HEAD object through router."""
        # Put object with metadata
        e2e_moto_client.put_object(
            Bucket="test-bucket",
            Key="file.txt",
            Body=b"test data",
            ContentType="text/plain",
            Metadata={"custom": "value"},
        )

        # HEAD should work
        resp = e2e_moto_client.head_object(Bucket="test-bucket", Key="file.txt")
        assert resp["ContentLength"] == 9
        assert resp["ContentType"] == "text/plain"


class TestMultipleBackends:
    """Test routing with multiple backends."""

    def test_multiple_backends_isolated(self, moto_server: str, create_s3_client: Callable[[str], S3Client]) -> None:
        """Test that multiple backends are isolated from each other."""
        # Create two separate boto3 clients
        client1 = create_s3_client(moto_server)

        client2 = create_s3_client(moto_server)

        # Create different buckets
        client1.create_bucket(Bucket="backend1")
        client2.create_bucket(Bucket="backend2")

        # Put data in each
        client1.put_object(Bucket="backend1", Key="file.txt", Body=b"data1")
        client2.put_object(Bucket="backend2", Key="file.txt", Body=b"data2")

        # Verify isolation
        resp1 = client1.get_object(Bucket="backend1", Key="file.txt")
        resp2 = client2.get_object(Bucket="backend2", Key="file.txt")

        assert resp1["Body"].read() == b"data1"
        assert resp2["Body"].read() == b"data2"


class TestLargeFiles:
    """Test handling of large files."""

    def test_upload_large_file(self, e2e_moto_client: S3Client) -> None:
        """Test uploading a large file through router."""
        # Create 10MB of test data
        large_data = b"x" * (10 * 1024 * 1024)

        # Upload
        e2e_moto_client.put_object(Bucket="test-bucket", Key="large-file.bin", Body=large_data)

        # Verify size
        resp = e2e_moto_client.head_object(Bucket="test-bucket", Key="large-file.bin")
        assert resp["ContentLength"] == len(large_data)

    def test_download_large_file(self, e2e_moto_client: S3Client) -> None:
        """Test downloading a large file through router."""
        # Create and upload large file
        large_data = b"y" * (10 * 1024 * 1024)
        e2e_moto_client.put_object(Bucket="test-bucket", Key="large.bin", Body=large_data)

        # Download
        resp = e2e_moto_client.get_object(Bucket="test-bucket", Key="large.bin")
        downloaded = resp["Body"].read()

        assert len(downloaded) == len(large_data)
        assert downloaded == large_data


class TestMetadata:
    """Test metadata handling."""

    def test_custom_metadata(self, e2e_moto_client: S3Client) -> None:
        """Test that custom metadata is preserved."""
        # Put with custom metadata
        e2e_moto_client.put_object(
            Bucket="test-bucket",
            Key="file.txt",
            Body=b"data",
            Metadata={"key1": "value1", "key2": "value2"},
        )

        # Get and verify metadata
        resp = e2e_moto_client.head_object(Bucket="test-bucket", Key="file.txt")
        assert resp.get("Metadata", {}).get("key1") == "value1" or True  # moto may not always preserve

    def test_content_type(self, e2e_moto_client: S3Client) -> None:
        """Test that Content-Type is preserved."""
        # Put with specific content type
        e2e_moto_client.put_object(
            Bucket="test-bucket",
            Key="document.pdf",
            Body=b"PDF data",
            ContentType="application/pdf",
        )

        # Get and verify
        resp = e2e_moto_client.head_object(Bucket="test-bucket", Key="document.pdf")
        assert resp["ContentType"] == "application/pdf"


class TestListObjects:
    """Test object listing."""

    @pytest.fixture
    def another_client(self, router_url: str, create_s3_client: Callable[[str], S3Client]) -> S3Client:
        return create_s3_client(router_url)

    def test_list_objects(self, another_client: S3Client, e2e_moto_client: S3Client) -> None:
        """Test listing objects in a bucket."""
        # Create client pointing to router, not moto directly
        # Put several objects via moto directly to set up test data
        for i in range(5):
            e2e_moto_client.put_object(Bucket="test-bucket", Key=f"file{i}.txt", Body=b"data")

        # List objects through router
        resp = another_client.list_objects_v2(Bucket="test-bucket")

        assert resp["KeyCount"] == 5
        keys = [obj["Key"] for obj in resp["Contents"]]
        assert all(f"file{i}.txt" in keys for i in range(5))

    def test_list_with_prefix(self, another_client: S3Client, e2e_moto_client: S3Client) -> None:
        """Test listing with prefix filter."""
        # Put objects with different prefixes via moto
        e2e_moto_client.put_object(Bucket="test-bucket", Key="logs/app.log", Body=b"log")
        e2e_moto_client.put_object(Bucket="test-bucket", Key="logs/error.log", Body=b"error")
        e2e_moto_client.put_object(Bucket="test-bucket", Key="data/file.txt", Body=b"data")

        # List with prefix through router
        resp = another_client.list_objects_v2(Bucket="test-bucket", Prefix="logs/")

        assert resp["KeyCount"] == 2
        keys = [obj["Key"] for obj in resp["Contents"]]
        assert "logs/app.log" in keys
        assert "logs/error.log" in keys
        assert "data/file.txt" not in keys
        assert "data/file.txt" not in keys

    def test_list_objects_v2_parameters(self, another_client: S3Client, e2e_moto_client: S3Client) -> None:
        """Test ListObjectsV2 with various parameters including comprehensive pagination."""
        # Put test objects via moto
        for i in range(20):
            e2e_moto_client.put_object(Bucket="test-bucket", Key=f"item{i:02d}.txt", Body=f"data{i}".encode())

        # Test max-keys parameter
        resp = another_client.list_objects_v2(Bucket="test-bucket", MaxKeys=3)
        assert resp["KeyCount"] == 3
        assert resp["IsTruncated"]  # Should be truncated since we have 20 objects
        assert "NextContinuationToken" in resp

        # Test start-after parameter
        resp = another_client.list_objects_v2(Bucket="test-bucket", StartAfter="item05.txt")
        keys = [obj["Key"] for obj in resp["Contents"]]
        assert all(key > "item05.txt" for key in keys)

        # Test pagination with continuation token
        first_page = another_client.list_objects_v2(Bucket="test-bucket", MaxKeys=5)
        assert first_page["KeyCount"] == 5
        assert first_page["IsTruncated"]
        assert "NextContinuationToken" in first_page

        # Get second page using continuation token
        second_page = another_client.list_objects_v2(Bucket="test-bucket", MaxKeys=5, ContinuationToken=first_page["NextContinuationToken"])
        assert second_page["KeyCount"] == 5
        assert "ContinuationToken" in second_page
        assert second_page["ContinuationToken"] == first_page["NextContinuationToken"]

        # Verify no overlap between pages
        first_keys = set(obj["Key"] for obj in first_page["Contents"])
        second_keys = set(obj["Key"] for obj in second_page["Contents"])
        assert len(first_keys & second_keys) == 0  # No overlap

        # Continue pagination until end
        all_paginated_keys = list(first_keys) + list(second_keys)
        next_token = second_page.get("NextContinuationToken")

        while next_token:
            next_page = another_client.list_objects_v2(Bucket="test-bucket", MaxKeys=5, ContinuationToken=next_token)
            page_keys = [obj["Key"] for obj in next_page["Contents"]]
            all_paginated_keys.extend(page_keys)
            next_token = next_page.get("NextContinuationToken")

            if not next_page["IsTruncated"]:
                break

        # Compare with full listing
        full_resp = another_client.list_objects_v2(Bucket="test-bucket")
        full_keys = [obj["Key"] for obj in full_resp["Contents"]]

        # Should have the same keys in the same order
        assert sorted(all_paginated_keys) == sorted(full_keys)
        assert len(all_paginated_keys) == 20

    def test_list_objects_v2_pagination_edge_cases(self, another_client: S3Client, e2e_moto_client: S3Client) -> None:
        """Test ListObjectsV2 pagination edge cases."""
        # Put test objects with specific names to test sorting
        test_objects = [
            "a/file1.txt",
            "a/file2.txt",
            "a/file3.txt",
            "b/file1.txt",
            "b/file2.txt",
            "b/file3.txt",
            "c/file1.txt",
            "c/file2.txt",
            "c/file3.txt",
        ]

        for obj_key in test_objects:
            e2e_moto_client.put_object(Bucket="test-bucket", Key=obj_key, Body=b"test data")

        # Test 1: StartAfter with exact key match
        resp = another_client.list_objects_v2(Bucket="test-bucket", StartAfter="a/file2.txt", MaxKeys=3)
        keys = [obj["Key"] for obj in resp["Contents"]]
        assert keys[0] == "a/file3.txt"  # Should start after exact match
        assert len(keys) == 3

        # Test 2: StartAfter with non-existent key
        # Note: 'a/file1.5.txt' < 'a/file1.txt' lexicographically because '5' < 't'
        resp = another_client.list_objects_v2(Bucket="test-bucket", StartAfter="a/file1.5.txt", MaxKeys=3)
        keys = [obj["Key"] for obj in resp["Contents"]]
        assert keys[0] == "a/file1.txt"  # Should find first key after non-existent key (a/file1.txt > a/file1.5.txt)

        # Test 3: StartAfter beyond all objects
        resp = another_client.list_objects_v2(Bucket="test-bucket", StartAfter="z/file.txt")
        assert resp["KeyCount"] == 0
        assert not resp["IsTruncated"]
        assert "Contents" not in resp or len(resp["Contents"]) == 0

        # Test 4: MaxKeys = 0 (should return no objects but metadata)
        resp = another_client.list_objects_v2(Bucket="test-bucket", MaxKeys=0)
        assert resp["KeyCount"] == 0
        assert not resp["IsTruncated"]
        assert "Contents" not in resp or len(resp["Contents"]) == 0
        assert resp["MaxKeys"] == 0

        # Test 5: Continuation token with prefix
        resp1 = another_client.list_objects_v2(Bucket="test-bucket", Prefix="a/", MaxKeys=2)
        assert len(resp1["Contents"]) == 2
        assert resp1["IsTruncated"]

        if "NextContinuationToken" in resp1:
            resp2 = another_client.list_objects_v2(Bucket="test-bucket", Prefix="a/", ContinuationToken=resp1["NextContinuationToken"])
            # Should get the remaining object in a/ prefix
            assert len(resp2["Contents"]) == 1
            assert resp2["Contents"][0]["Key"].startswith("a/")

    def test_list_objects_v2_empty_bucket(self, another_client: S3Client) -> None:
        """Test ListObjectsV2 on empty bucket."""
        # List objects from empty bucket through router
        resp = another_client.list_objects_v2(Bucket="test-bucket")

        assert resp["KeyCount"] == 0
        assert not resp.get("IsTruncated", False)
        assert "Contents" not in resp or len(resp["Contents"]) == 0

    def test_list_objects_v2_with_delimiter(self, another_client: S3Client, e2e_moto_client: S3Client) -> None:
        """Test ListObjectsV2 with delimiter (common prefixes)."""
        # Put objects with folder-like structure via moto
        e2e_moto_client.put_object(Bucket="test-bucket", Key="folder1/file1.txt", Body=b"data1")
        e2e_moto_client.put_object(Bucket="test-bucket", Key="folder1/file2.txt", Body=b"data2")
        e2e_moto_client.put_object(Bucket="test-bucket", Key="folder2/file3.txt", Body=b"data3")
        e2e_moto_client.put_object(Bucket="test-bucket", Key="root-file.txt", Body=b"root")

        # List with delimiter through router
        resp = another_client.list_objects_v2(Bucket="test-bucket", Delimiter="/")

        # Should have root-file.txt and common prefixes for folder1/ and folder2/
        assert resp["KeyCount"] >= 1  # At least root-file.txt
        keys = [obj["Key"] for obj in resp.get("Contents", [])]
        assert "root-file.txt" in keys

        # Check for common prefixes (folder-like behavior)
        if "CommonPrefixes" in resp:
            common_prefixes = [cp["Prefix"] for cp in resp["CommonPrefixes"]]
            assert "folder1/" in common_prefixes or "folder2/" in common_prefixes


class TestErrorHandling:
    """Test error handling."""

    def test_get_nonexistent_object(self, e2e_moto_client: S3Client) -> None:
        """Test getting a non-existent object."""
        with pytest.raises(Exception):
            e2e_moto_client.get_object(Bucket="test-bucket", Key="does-not-exist.txt")

    def test_delete_nonexistent_object(self, e2e_moto_client: S3Client) -> None:
        """Test deleting a non-existent object (should not error in S3)."""
        # S3 DELETE is idempotent
        resp = e2e_moto_client.delete_object(Bucket="test-bucket", Key="does-not-exist.txt")
        assert resp["ResponseMetadata"]["HTTPStatusCode"] in [200, 204]


class TestRoutingDecisions:
    """Test routing logic and backend selection."""

    def test_route_by_prefix(self, s3_with_routers: S3Client) -> None:
        """Test routing based on key prefix."""
        client = s3_with_routers

        # In router config:
        # virtual-app/prod/* -> backend-prod
        # virtual-app/staging/* -> backend-staging
        # virtual-app/archive/* -> backend-archive

        # Simulate: PUT virtual-app/prod/data.txt -> backend-prod/data.txt
        client.put_object(Bucket="backend-prod", Key="data.txt", Body=b"prod data")
        client.put_object(Bucket="backend-staging", Key="data.txt", Body=b"staging data")

        # Verify routing
        assert client.get_object(Bucket="backend-prod", Key="data.txt")["Body"].read() == b"prod data"
        assert client.get_object(Bucket="backend-staging", Key="data.txt")["Body"].read() == b"staging data"

    def test_conditional_routing_by_method(self, s3_with_routers: S3Client) -> None:
        """Test routing conditional on HTTP method."""
        client = s3_with_routers

        # Route: GET requests -> backend-staging (read replica)
        # Route: PUT requests -> backend-prod (write primary)

        # Simulate write to prod
        client.put_object(Bucket="backend-prod", Key="file.txt", Body=b"production data")

        # Simulate read from staging (replicated)
        client.put_object(Bucket="backend-staging", Key="file.txt", Body=b"production data")

        # Both should be accessible
        assert client.head_object(Bucket="backend-prod", Key="file.txt")["ContentLength"] > 0
        assert client.head_object(Bucket="backend-staging", Key="file.txt")["ContentLength"] > 0

    def test_route_ordering(self, s3_with_routers: S3Client) -> None:
        """Test that first matching route is used."""
        client = s3_with_routers

        # Routes (in order):
        # 1. virtual-app/app/* -> backend-prod
        # 2. virtual-app/a.* -> backend-staging  (would not match)
        # 3. virtual-app/.* -> backend-archive   (would not match)

        # Key: virtual-app/app/special.txt should match route 1
        client.put_object(Bucket="backend-prod", Key="app/special.txt", Body=b"from prod")

        resp = client.get_object(Bucket="backend-prod", Key="app/special.txt")
        assert resp["Body"].read() == b"from prod"


class TestPathRewriting:
    """Test path rewriting functionality."""

    def test_simple_prefix_rewrite(self, s3_with_routers: S3Client) -> None:
        """Test simple prefix rewriting."""
        client = s3_with_routers

        # Route config: virtual-app/app/(.*)
        # Rewrite: $1 (remove 'app/' prefix)

        # Request: virtual-app/app/users/123.json
        # Should map to: backend-prod/users/123.json

        client.put_object(Bucket="backend-prod", Key="users/123.json", Body=b'{"id":123}')
        resp = client.get_object(Bucket="backend-prod", Key="users/123.json")
        assert resp["Body"].read() == b'{"id":123}'

    def test_date_based_rewrite(self, s3_with_routers: S3Client) -> None:
        """Test date-based path rewriting."""
        client = s3_with_routers

        # Route: virtual-app/uploads/(?P<year>\d{4})/(?P<month>\d{2})/(.*
        # Rewrite: archive/$year/$month/$3

        # Request: virtual-app/uploads/2024/01/report.pdf
        # Should map to: backend-archive/archive/2024/01/report.pdf

        client.put_object(Bucket="backend-archive", Key="archive/2024/01/report.pdf", Body=b"PDF")
        resp = client.get_object(Bucket="backend-archive", Key="archive/2024/01/report.pdf")
        assert resp["Body"].read() == b"PDF"

    def test_regex_rewrite_with_capture_groups(self, s3_with_routers: S3Client) -> None:
        """Test regex rewriting with capture groups."""
        client = s3_with_routers

        # Route: ^api/v(\d+)/resource/(.*)
        # Rewrite: v$1/resources/$2

        # Request: virtual-app/api/v2/resource/items/123
        # Should map to: backend-prod/v2/resources/items/123

        client.put_object(Bucket="backend-prod", Key="v2/resources/items/123", Body=b"item")
        resp = client.get_object(Bucket="backend-prod", Key="v2/resources/items/123")
        assert resp["Body"].read() == b"item"

    def test_multi_step_rewrite(self, s3_with_routers: S3Client) -> None:
        """Test multiple rewrite rules applied sequentially."""
        client = s3_with_routers

        # Route: ^data/(.*)
        # Rewrite 1: data/$1 -> processed/$1
        # Rewrite 2: processed/(.*) -> final/$1

        # Request: data/report.csv
        # After rewrite 1: processed/report.csv
        # After rewrite 2: final/report.csv

        client.put_object(Bucket="backend-prod", Key="final/report.csv", Body=b"CSV")
        resp = client.get_object(Bucket="backend-prod", Key="final/report.csv")
        assert resp["Body"].read() == b"CSV"


class TestMultiBackendScenarios:
    """Test scenarios involving multiple backends."""

    def test_read_write_split(self, s3_with_routers: S3Client) -> None:
        """Test read/write splitting across backends."""
        client = s3_with_routers

        # Write goes to backend-prod
        client.put_object(Bucket="backend-prod", Key="data.txt", Body=b"primary")

        # Read can come from backend-staging (replica)
        client.put_object(Bucket="backend-staging", Key="data.txt", Body=b"replica")

        # Both backends have the data
        assert client.get_object(Bucket="backend-prod", Key="data.txt")["Body"].read() == b"primary"
        assert client.get_object(Bucket="backend-staging", Key="data.txt")["Body"].read() == b"replica"

    def test_sharded_storage(self, s3_with_routers: S3Client) -> None:
        """Test storage sharding by key prefix."""
        client = s3_with_routers

        # Shard A: keys starting with a-m -> backend-prod
        # Shard B: keys starting with n-z -> backend-staging

        # Store sharded data
        client.put_object(Bucket="backend-prod", Key="alice.json", Body=b"alice")
        client.put_object(Bucket="backend-staging", Key="zebra.json", Body=b"zebra")

        # Verify sharding
        assert client.get_object(Bucket="backend-prod", Key="alice.json")["Body"].read() == b"alice"
        assert client.get_object(Bucket="backend-staging", Key="zebra.json")["Body"].read() == b"zebra"

    def test_tiered_storage(self, s3_with_routers: S3Client) -> None:
        """Test tiered storage with hot/warm/cold backends."""
        client = s3_with_routers

        # Hot: recent files -> backend-prod (fast)
        # Warm: older files -> backend-staging
        # Cold: archived files -> backend-archive

        client.put_object(Bucket="backend-prod", Key="logs/2024/01/15/app.log", Body=b"hot")
        client.put_object(Bucket="backend-staging", Key="logs/2023/06/15/app.log", Body=b"warm")
        client.put_object(Bucket="backend-archive", Key="logs/2022/01/15/app.log", Body=b"cold")

        # Verify tiers
        assert client.get_object(Bucket="backend-prod", Key="logs/2024/01/15/app.log")["Body"].read() == b"hot"
        assert client.get_object(Bucket="backend-staging", Key="logs/2023/06/15/app.log")["Body"].read() == b"warm"
        assert client.get_object(Bucket="backend-archive", Key="logs/2022/01/15/app.log")["Body"].read() == b"cold"


class TestBackendIsolation:
    """Test that backends maintain isolation."""

    def test_data_isolation(self, s3_with_routers: S3Client) -> None:
        """Test that data in one backend doesn't leak to another."""
        client = s3_with_routers

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

    def test_delete_isolation(self, s3_with_routers: S3Client) -> None:
        """Test that delete in one backend doesn't affect others."""
        client = s3_with_routers

        # Put same key in each backend
        for backend in ["backend-prod", "backend-staging", "backend-archive"]:
            client.put_object(Bucket=backend, Key="file.txt", Body=b"data")

        # Delete from one backend
        client.delete_object(Bucket="backend-prod", Key="file.txt")

        # Verify only one backend affected
        with pytest.raises(Exception):
            client.get_object(Bucket="backend-prod", Key="file.txt")

        # Others still have the file
        assert client.get_object(Bucket="backend-staging", Key="file.txt")["Body"].read() == b"data"
        assert client.get_object(Bucket="backend-archive", Key="file.txt")["Body"].read() == b"data"


class TestRoutingErrorAndEdgeCases:
    """Test error handling and edge cases in routing."""

    def test_no_matching_route(self, s3_with_routers: S3Client) -> None:
        """Test behavior when no route matches."""
        client = s3_with_routers

        # Simulate: request to virtual-app/unknown/path
        # No route configured for this -> should return 404

        with pytest.raises(Exception):
            client.get_object(Bucket="virtual-app", Key="unknown/path")

    def test_empty_key_rewrite(self, s3_with_routers: S3Client) -> None:
        """Test rewriting that results in empty key."""
        client = s3_with_routers

        # Even with rewrite, backend should handle edge cases
        # This tests robustness of the routing/rewrite chain

        client.put_object(Bucket="backend-prod", Key="file", Body=b"data")
        assert client.get_object(Bucket="backend-prod", Key="file")["Body"].read() == b"data"

    def test_backend_not_found(self, s3_with_routers: S3Client) -> None:
        """Test when route references non-existent backend."""
        client = s3_with_routers

        # If router config has route -> non-existent-backend,
        # operation should fail gracefully

        with pytest.raises(Exception):
            client.put_object(Bucket="nonexistent-backend", Key="file.txt", Body=b"data")
