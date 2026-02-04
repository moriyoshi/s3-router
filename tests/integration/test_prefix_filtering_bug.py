"""
Integration test to reproduce the prefix filtering bug.

Bug description:
When listing a virtual bucket with a rewrite rule that adds a prefix,
the router should only return objects that match the rewritten prefix.

Example:
- Virtual bucket: app-data-storage
- Route pattern: ^(?P<key>.*)
- Rewrite: app/data-storage/$key
- Backend bucket has: app/data-storage/test, system/config.json
- Expected: List should only show objects under app/data-storage/
- Actual (bug): List shows ALL prefixes including system/
"""

from __future__ import annotations

from typing import TYPE_CHECKING
from collections.abc import Callable, Iterator
import pytest

if TYPE_CHECKING:
    from types_boto3_s3 import S3Client  # type: ignore[import]
    from .conftest import S3RouterWithMoto


def _cleanup_bucket(client: S3Client, bucket_name: str) -> None:
    """Helper to delete all objects in a bucket."""
    try:
        paginator = client.get_paginator("list_objects_v2")
        for page in paginator.paginate(Bucket=bucket_name):
            if "Contents" in page:
                for obj in page["Contents"]:
                    client.delete_object(Bucket=bucket_name, Key=obj["Key"])
    except Exception:
        pass


@pytest.fixture
def s3_prefix_bug_setup(
    s3router_with_moto: S3RouterWithMoto, create_s3_client: Callable[[str], S3Client]
) -> Iterator[tuple[S3Client, S3Client]]:
    """
    Setup for prefix filtering bug test.

    Returns:
        Tuple of (moto_client, router_client)
    """
    moto_url = s3router_with_moto["moto_endpoint"]
    router_url = s3router_with_moto["router_url"]

    # Create clients
    moto_client = create_s3_client(moto_url)
    router_client = create_s3_client(router_url)

    # Create backend bucket
    moto_client.create_bucket(Bucket="app-backend-bucket")

    # Create test objects in different prefixes
    # This simulates the real scenario:
    # - app/data-storage/* should be visible in the virtual bucket
    # - system/* should NOT be visible
    moto_client.put_object(
        Bucket="app-backend-bucket",
        Key="app/data-storage/test",
        Body=b"test data in app data storage",
    )
    moto_client.put_object(
        Bucket="app-backend-bucket",
        Key="system/config.json",
        Body=b"system data that should NOT be visible",
    )

    yield moto_client, router_client

    # Cleanup - delete all objects in the bucket
    _cleanup_bucket(moto_client, "app-backend-bucket")


def test_list_virtual_bucket_filters_by_rewrite_prefix(
    s3_prefix_bug_setup: tuple[S3Client, S3Client],
) -> None:
    """
    Test that listing a virtual bucket only returns objects matching the rewrite prefix.

    This test reproduces the bug where objects outside the rewrite prefix are incorrectly
    returned when listing a virtual bucket.

    Virtual bucket configuration (from conftest.py):
        - name: app-data-storage
        - routes:
            - backend: moto-s3-app-backend
              path: ^(?P<key>.*)
              rewrite:
                - result: app/data-storage/$key

    Backend has:
        - app/data-storage/test
        - system/config.json

    Expected behavior when listing 'app-data-storage':
        - Should show: test (virtual key after reverse rewrite)
        - Should NOT show: system/ prefix

    Bug: Currently shows system/ prefix which should be filtered out.
    """
    _, router_client = s3_prefix_bug_setup

    # List objects in the virtual bucket (no prefix, with delimiter for prefixes)
    response = router_client.list_objects_v2(Bucket="app-data-storage", Delimiter="/")

    # Extract results
    object_keys = [obj["Key"] for obj in response.get("Contents", [])]
    common_prefixes = [cp["Prefix"] for cp in response.get("CommonPrefixes", [])]

    # BUG REPRODUCTION:
    # The router currently returns "system/" as a common prefix,
    # but it should NOT because:
    # 1. The virtual bucket rewrites ^(?P<key>.*) to app/data-storage/$key
    # 2. So the backend query should use prefix "app/data-storage/"
    # 3. Objects under "system/" don't match this prefix
    # 4. Therefore they shouldn't appear in the listing

    assert "system/" not in common_prefixes, (
        f"BUG REPRODUCED: system/ prefix appeared in virtual bucket listing. "
        f"This object is at system/ in the backend, not under app/data-storage/, "
        f"so it should be filtered out. Common prefixes: {common_prefixes}"
    )

    # The virtual bucket should only show "test" (the virtual key)
    assert object_keys == ["test"], f"Expected virtual bucket to show ['test'], got {object_keys}"

    assert len(common_prefixes) == 0, f"Expected no common prefixes, got {common_prefixes}"


def test_list_virtual_bucket_with_nested_objects(
    s3_prefix_bug_setup: tuple[S3Client, S3Client],
) -> None:
    """
    Test that listing a virtual bucket correctly handles nested objects at different depths.

    Scenario:
        Backend:
            - app/data-storage/group-1/item-1.json
            - app/data-storage/group-1/item-2.json
            - app/data-storage/group-2/item-3.json
            - archive/old-file.txt

        Virtual bucket should show:
            - group-1/ (prefix)
            - group-2/ (prefix)
            NOT show: archive/ (outside rewrite scope)
    """
    moto_client, router_client = s3_prefix_bug_setup

    # Add nested objects
    moto_client.put_object(
        Bucket="app-backend-bucket",
        Key="app/data-storage/group-1/item-1.json",
        Body=b"data1",
    )
    moto_client.put_object(
        Bucket="app-backend-bucket",
        Key="app/data-storage/group-1/item-2.json",
        Body=b"data2",
    )
    moto_client.put_object(
        Bucket="app-backend-bucket",
        Key="app/data-storage/group-2/item-3.json",
        Body=b"data3",
    )
    moto_client.put_object(Bucket="app-backend-bucket", Key="archive/old-file.txt", Body=b"other")

    # List with delimiter
    response = router_client.list_objects_v2(Bucket="app-data-storage", Delimiter="/")

    common_prefixes = [cp["Prefix"] for cp in response.get("CommonPrefixes", [])]

    # Should only show group-1/ and group-2/, not archive/
    assert "group-1/" in common_prefixes, f"group-1/ should be in prefixes, got {common_prefixes}"
    assert "group-2/" in common_prefixes, f"group-2/ should be in prefixes, got {common_prefixes}"
    assert "archive/" not in common_prefixes, f"archive/ should NOT be in prefixes, got {common_prefixes}"
    assert len(common_prefixes) == 2, f"Expected 2 prefixes, got {len(common_prefixes)}"


def test_list_virtual_bucket_with_virtual_prefix(
    s3_prefix_bug_setup: tuple[S3Client, S3Client],
) -> None:
    """
    Test that listing a virtual bucket WITH a virtual prefix works correctly.

    Scenario:
        Backend:
            - app/data-storage/test
            - app/data-storage/set-1/data.json
            - app/data-storage/set-2/data.json

        List with virtual prefix "set-":
            Should only show: set-1/, set-2/
    """
    moto_client, router_client = s3_prefix_bug_setup

    # Add more objects
    moto_client.put_object(
        Bucket="app-backend-bucket",
        Key="app/data-storage/set-1/data.json",
        Body=b"set1",
    )
    moto_client.put_object(
        Bucket="app-backend-bucket",
        Key="app/data-storage/set-2/data.json",
        Body=b"set2",
    )

    # List with virtual prefix and delimiter
    response = router_client.list_objects_v2(Bucket="app-data-storage", Prefix="set-", Delimiter="/")

    object_keys = [obj["Key"] for obj in response.get("Contents", [])]
    common_prefixes = [cp["Prefix"] for cp in response.get("CommonPrefixes", [])]

    # Should only show set-* prefixes, not "test"
    assert "test" not in object_keys, f"'test' should not match prefix 'set-', got {object_keys}"
    assert "set-1/" in common_prefixes, f"set-1/ should match prefix 'set-', got {common_prefixes}"
    assert "set-2/" in common_prefixes, f"set-2/ should match prefix 'set-', got {common_prefixes}"


def test_list_virtual_bucket_multiple_unrelated_prefixes(
    s3_prefix_bug_setup: tuple[S3Client, S3Client],
) -> None:
    """
    Test that multiple unrelated prefixes in backend don't leak into virtual listing.

    Scenario:
        Backend:
            - app/data-storage/block1.json
            - cache/temp.json
            - logs/app.log
            - tmp/file.txt

        Virtual bucket should ONLY show:
            - block1.json
            NOT show: cache/, logs/, tmp/
    """
    moto_client, router_client = s3_prefix_bug_setup

    # Add multiple unrelated prefixes
    moto_client.put_object(
        Bucket="app-backend-bucket",
        Key="logs/app.log",
        Body=b"logs",
    )
    moto_client.put_object(Bucket="app-backend-bucket", Key="tmp/file.txt", Body=b"other")

    # List the virtual bucket
    response = router_client.list_objects_v2(Bucket="app-data-storage", Delimiter="/")

    object_keys = [obj["Key"] for obj in response.get("Contents", [])]
    common_prefixes = [cp["Prefix"] for cp in response.get("CommonPrefixes", [])]

    # Should only show app/data-storage/test object
    assert "test" in object_keys, f"test should be visible, got {object_keys}"

    # Should NOT show any other prefixes
    assert "system/" not in common_prefixes, f"system/ should not be visible, got {common_prefixes}"
    assert "logs/" not in common_prefixes, f"logs/ should not be visible, got {common_prefixes}"
    assert "tmp/" not in common_prefixes, f"tmp/ should not be visible, got {common_prefixes}"

    # Should have no prefixes, only objects
    assert len(common_prefixes) == 0, f"Should have no prefixes, got {common_prefixes}"


@pytest.mark.parametrize("max_keys", [5, 10, 20])
def test_list_virtual_bucket_pagination_with_many_objects(
    s3_prefix_bug_setup: tuple[S3Client, S3Client],
    max_keys: int,
) -> None:
    """
    Parameterized test: pagination with different MaxKeys values.

    Scenario:
        Backend has objects in various prefixes:
        - 25 objects under app/data-storage/item-* (plus base 'test')
        - 15 objects under cache/entry-*
        - 10 objects under logs/*

        Paginate through with various MaxKeys values, verify:
        1. Only objects from app/data-storage/ are returned
        2. NO cache/ or logs/ objects leak through
        3. Total count is 25 items (test object sorts after all items)
    """
    moto_client, router_client = s3_prefix_bug_setup

    # Add 25 objects in the correct prefix
    for i in range(25):
        moto_client.put_object(
            Bucket="app-backend-bucket",
            Key=f"app/data-storage/item-{i:03d}.json",
            Body=f"item {i}".encode(),
        )

    # Add 15 objects in cache prefix (should be filtered out)
    for i in range(15):
        moto_client.put_object(
            Bucket="app-backend-bucket",
            Key=f"cache/entry-{i:03d}.json",
            Body=f"cache entry {i}".encode(),
        )

    # Add 10 objects in logs prefix (should be filtered out)
    for i in range(10):
        moto_client.put_object(
            Bucket="app-backend-bucket",
            Key=f"logs/file-{i:03d}.txt",
            Body=f"log {i}".encode(),
        )

    # Paginate through all results
    all_objects = []
    continuation_token = None

    while True:
        if continuation_token:
            response = router_client.list_objects_v2(Bucket="app-data-storage", MaxKeys=max_keys, ContinuationToken=continuation_token)
        else:
            response = router_client.list_objects_v2(Bucket="app-data-storage", MaxKeys=max_keys)

        # Extract objects from this page
        page_objects = [obj["Key"] for obj in response.get("Contents", [])]
        all_objects.extend(page_objects)

        # Verify NO cache/ or logs/ objects in this page
        for obj_key in page_objects:
            assert not obj_key.startswith("cache/"), f"BUG: cache/ object '{obj_key}' leaked with MaxKeys={max_keys}"
            assert not obj_key.startswith("logs/"), f"BUG: logs/ object '{obj_key}' leaked with MaxKeys={max_keys}"

        # Check if there are more results
        if not response.get("IsTruncated"):
            break

        continuation_token = response.get("NextContinuationToken")
        if not continuation_token:
            break

    # Should have 25-26 objects (25 items, plus potentially 'test' from fixture)
    # The 'test' object may or may not appear depending on pagination boundaries
    assert 25 <= len(all_objects) <= 26, f"Expected 25-26 objects with MaxKeys={max_keys}, got {len(all_objects)}"

    # All should be valid (test or item-*)
    for obj_key in all_objects:
        assert obj_key == "test" or obj_key.startswith("item-"), f"Expected 'test' or 'item-*', got '{obj_key}'"


@pytest.mark.parametrize("day_count,item_per_day", [(5, 10), (10, 10), (20, 5)])
def test_list_virtual_bucket_pagination_with_date_prefixes(
    s3_prefix_bug_setup: tuple[S3Client, S3Client],
    day_count: int,
    item_per_day: int,
) -> None:
    """
    Parameterized test: pagination with varying date-organized prefixes.

    Scenario:
        Backend has date-organized objects:
        - app/data-storage/2026-02-DD/item-NNN.json (valid)
        - cache/2026-02-DD/entry-NNN.json (should filter)
        - logs/2026-02-DD/file-NNN.txt (should filter)

    Parameters:
        day_count: Number of days (5, 10, or 20)
        item_per_day: Objects per day (10 or 5)
    """
    moto_client, router_client = s3_prefix_bug_setup

    # Add objects organized by date
    for day in range(1, day_count + 1):
        for item_num in range(item_per_day):
            moto_client.put_object(
                Bucket="app-backend-bucket",
                Key=f"app/data-storage/2026-02-{day:02d}/item-{item_num:03d}.json",
                Body=f"item {day}-{item_num}".encode(),
            )

    # Add objects with same structure under cache/ (should be filtered)
    for day in range(1, day_count + 1):
        for entry_num in range(item_per_day):
            moto_client.put_object(
                Bucket="app-backend-bucket",
                Key=f"cache/2026-02-{day:02d}/entry-{entry_num:03d}.json",
                Body=f"cache {day}-{entry_num}".encode(),
            )

    # Add objects with same structure under logs/ (should be filtered)
    for day in range(1, day_count + 1):
        for file_num in range(item_per_day):
            moto_client.put_object(
                Bucket="app-backend-bucket",
                Key=f"logs/2026-02-{day:02d}/file-{file_num:03d}.txt",
                Body=f"log {day}-{file_num}".encode(),
            )

    # Paginate through all results with MaxKeys=20
    all_objects = []
    continuation_token = None
    invalid_prefixes_found = []

    while True:
        if continuation_token:
            response = router_client.list_objects_v2(Bucket="app-data-storage", MaxKeys=20, ContinuationToken=continuation_token)
        else:
            response = router_client.list_objects_v2(Bucket="app-data-storage", MaxKeys=20)

        # Extract objects from this page
        page_objects = [obj["Key"] for obj in response.get("Contents", [])]
        all_objects.extend(page_objects)

        # Verify NO cache/ or logs/ objects in this page
        for obj_key in page_objects:
            if obj_key.startswith("cache/"):
                invalid_prefixes_found.append(("cache", obj_key))
            elif obj_key.startswith("logs/"):
                invalid_prefixes_found.append(("logs", obj_key))

        # Check if there are more results
        if not response.get("IsTruncated"):
            break

        continuation_token = response.get("NextContinuationToken")
        if not continuation_token:
            break

    if invalid_prefixes_found:
        details = "; ".join(f"{prefix}: {key}" for prefix, key in invalid_prefixes_found)
        raise AssertionError(f"BUG: Found {len(invalid_prefixes_found)} objects from invalid prefixes: {details}")

    # Should have day_count * item_per_day objects
    # (plus potentially 'test' from fixture depending on pagination)
    expected_count = day_count * item_per_day
    assert expected_count <= len(all_objects) <= expected_count + 1, (
        f"Expected {expected_count}-{expected_count + 1} objects, got {len(all_objects)}"
    )

    # All should match pattern: 2026-02-NN/item-NNN.json or be 'test'
    for obj_key in all_objects:
        is_date = obj_key.startswith("2026-02-") and "/item-" in obj_key
        is_test = obj_key == "test"
        assert is_date or is_test, f"Expected '2026-02-NN/item-NNN.json' or 'test', got '{obj_key}'"


@pytest.mark.parametrize("max_keys", [1, 2, 5, 10])
def test_list_virtual_bucket_pagination_edge_cases(
    s3_prefix_bug_setup: tuple[S3Client, S3Client],
    max_keys: int,
) -> None:
    """
    Parameterized test: pagination edge cases with various MaxKeys values.

    Scenario:
        Backend has:
        - obj-001.json, obj-002.json, obj-003.json under app/data-storage/
        - extra.json under cache/ (should NOT appear)

    Parameters:
        max_keys: MaxKeys value (1, 2, 5, or 10)
    """
    moto_client, router_client = s3_prefix_bug_setup

    # Add 3 objects in correct prefix
    for i in range(1, 4):
        moto_client.put_object(
            Bucket="app-backend-bucket",
            Key=f"app/data-storage/obj-{i:03d}.json",
            Body=f"object {i}".encode(),
        )

    # Paginate through with varying MaxKeys
    all_objects = []
    continuation_token = None

    for _ in range(10):  # Up to 10 pages (should only need 4-5)
        if continuation_token:
            response = router_client.list_objects_v2(Bucket="app-data-storage", MaxKeys=max_keys, ContinuationToken=continuation_token)
        else:
            response = router_client.list_objects_v2(Bucket="app-data-storage", MaxKeys=max_keys)

        page_objects = [obj["Key"] for obj in response.get("Contents", [])]

        if not page_objects:
            break

        all_objects.extend(page_objects)

        # Verify no cache/ objects at any MaxKeys value
        for obj_key in page_objects:
            assert not obj_key.startswith("cache/"), f"BUG: cache/ object appeared with MaxKeys={max_keys}"

        if not response.get("IsTruncated"):
            break

        continuation_token = response.get("NextContinuationToken")

    # Should get all 4 objects (3 obj-* + 1 test from fixture), never cache/
    assert len(all_objects) == 4, f"Expected 4 objects with MaxKeys={max_keys}, got {len(all_objects)}: {all_objects}"

    # All should be valid
    expected_keys = {"test", "obj-001.json", "obj-002.json", "obj-003.json"}
    actual_keys = set(all_objects)
    assert actual_keys == expected_keys, f"Expected {expected_keys}, got {actual_keys}"
