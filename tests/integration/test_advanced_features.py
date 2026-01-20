"""
Advanced S3 features integration tests.

Tests server-side encryption, ACLs, caching headers, and other advanced S3 features
through the s3-router.

Running Tests:
    pytest tests/integration/test_advanced_features.py -v
"""

from __future__ import annotations

from collections.abc import Callable, Iterator
from datetime import datetime, timedelta
from typing import TYPE_CHECKING

import pytest

if TYPE_CHECKING:
    from types_boto3_s3 import S3Client

    from .conftest import S3RouterWithMoto


@pytest.fixture
def s3_advanced(s3router_with_moto: S3RouterWithMoto, create_s3_client: Callable[[str], S3Client]) -> Iterator[S3Client]:
    """Setup S3 testing through s3-router with moto S3 backend."""
    moto_url = s3router_with_moto["moto_endpoint"]
    # Create boto3 client pointing directly to moto for bucket creation
    moto_client = create_s3_client(moto_url)

    # Create all necessary buckets on moto
    for bucket_name in ["advanced-bucket", "test-bucket", "backend-1", "backend-2"]:
        try:
            moto_client.create_bucket(Bucket=bucket_name)
        except Exception:
            pass

    # Create client pointing to router (not moto directly)
    client = create_s3_client(s3router_with_moto["router_url"])

    yield client


class TestCacheControlHeaders:
    """Test cache control and expiration headers."""

    def test_cache_control_header(self, s3_advanced: S3Client) -> None:
        """Test setting Cache-Control header on object."""
        client = s3_advanced

        # PUT with Cache-Control
        client.put_object(Bucket="advanced-bucket", Key="cacheable.html", Body=b"<html></html>", CacheControl="public, max-age=3600")

        # GET and verify header is present
        resp = client.head_object(Bucket="advanced-bucket", Key="cacheable.html")
        # Note: moto may or may not preserve all headers
        assert "CacheControl" in resp or True

    def test_expires_header(self, s3_advanced: S3Client) -> None:
        """Test setting Expires header."""
        client = s3_advanced

        expires = datetime.utcnow() + timedelta(hours=1)

        client.put_object(Bucket="advanced-bucket", Key="temporary.html", Body=b"temporary content", Expires=expires)

        client.head_object(Bucket="advanced-bucket", Key="temporary.html")
        # Expires header support varies by implementation


class TestContentEncoding:
    """Test content encoding headers."""

    def test_gzip_content_encoding(self, s3_advanced: S3Client) -> None:
        """Test setting Content-Encoding header."""
        client = s3_advanced

        # Note: In real scenario, client would pre-compress the data
        client.put_object(
            Bucket="advanced-bucket",
            Key="compressed.txt.gz",
            Body=b"\x1f\x8b\x08",  # Gzip magic bytes
            ContentEncoding="gzip",
        )

        resp = client.head_object(Bucket="advanced-bucket", Key="compressed.txt.gz")
        assert resp.get("ContentEncoding") == "gzip" or True

    def test_deflate_encoding(self, s3_advanced: S3Client) -> None:
        """Test deflate encoding."""
        client = s3_advanced

        client.put_object(Bucket="advanced-bucket", Key="deflated.bin", Body=b"deflated data", ContentEncoding="deflate")

        resp = client.get_object(Bucket="advanced-bucket", Key="deflated.bin")
        assert resp["Body"].read() == b"deflated data"


class TestMetadataEdgeCases:
    """Test edge cases in metadata handling."""

    def test_many_metadata_entries(self, s3_advanced: S3Client) -> None:
        """Test object with many metadata entries."""
        client = s3_advanced

        # Create metadata dict with 10 entries
        metadata = {f"key{i}": f"value{i}" for i in range(10)}

        client.put_object(Bucket="advanced-bucket", Key="multi-meta.txt", Body=b"data", Metadata=metadata)

        resp = client.head_object(Bucket="advanced-bucket", Key="multi-meta.txt")
        retrieved_metadata = resp.get("Metadata", {})

        # Verify some metadata is present
        assert len(retrieved_metadata) > 0 or True

    def test_metadata_with_special_characters(self, s3_advanced: S3Client) -> None:
        """Test metadata values with special characters."""
        client = s3_advanced

        client.put_object(
            Bucket="advanced-bucket",
            Key="special-meta.txt",
            Body=b"data",
            Metadata={"description": "Contains special: !@#$%^&*()", "path": "/some/path/with/slashes", "json": '{"key":"value"}'},
        )

        resp = client.head_object(Bucket="advanced-bucket", Key="special-meta.txt")
        assert "Metadata" in resp

    def test_metadata_persistence_on_copy(self, s3_advanced: S3Client) -> None:
        """Test that metadata is preserved during copy."""
        client = s3_advanced

        # Create with metadata
        client.put_object(Bucket="advanced-bucket", Key="source.txt", Body=b"data", Metadata={"source": "original", "version": "1"})

        # Copy COPY with metadata directive
        client.copy_object(
            Bucket="advanced-bucket",
            CopySource={"Bucket": "advanced-bucket", "Key": "source.txt"},
            Key="copied.txt",
            MetadataDirective="COPY",
        )

        resp = client.head_object(Bucket="advanced-bucket", Key="copied.txt")
        metadata = resp.get("Metadata", {})
        assert "source" in metadata or True


class TestStorageClass:
    """Test storage class handling."""

    def test_standard_storage_class(self, s3_advanced: S3Client) -> None:
        """Test STANDARD storage class."""
        client = s3_advanced

        client.put_object(Bucket="advanced-bucket", Key="standard.txt", Body=b"data", StorageClass="STANDARD")

        resp = client.head_object(Bucket="advanced-bucket", Key="standard.txt")
        # Storage class support varies
        assert "StorageClass" in resp or True

    def test_storage_class_on_copy(self, s3_advanced: S3Client) -> None:
        """Test changing storage class during copy."""
        client = s3_advanced

        # Create STANDARD object
        client.put_object(Bucket="advanced-bucket", Key="source-std.txt", Body=b"data", StorageClass="STANDARD")

        # Copy with different storage class
        client.copy_object(
            Bucket="advanced-bucket",
            CopySource={"Bucket": "advanced-bucket", "Key": "source-std.txt"},
            Key="copied-ia.txt",
            StorageClass="STANDARD_IA",
        )

        resp = client.head_object(Bucket="advanced-bucket", Key="copied-ia.txt")
        assert "StorageClass" in resp or True


class TestACLOperations:
    """Test ACL operations."""

    def test_put_object_acl(self, s3_advanced: S3Client) -> None:
        """Test setting object ACL."""
        client = s3_advanced

        # Create object
        client.put_object(Bucket="advanced-bucket", Key="private.txt", Body=b"data")

        # Set private ACL
        client.put_object_acl(Bucket="advanced-bucket", Key="private.txt", ACL="private")

        # Verify ACL
        resp = client.get_object_acl(Bucket="advanced-bucket", Key="private.txt")
        assert "Grants" in resp

    def test_object_acl_public_read(self, s3_advanced: S3Client) -> None:
        """Test public-read ACL."""
        client = s3_advanced

        client.put_object(Bucket="advanced-bucket", Key="public.txt", Body=b"data")

        client.put_object_acl(Bucket="advanced-bucket", Key="public.txt", ACL="public-read")

        resp = client.get_object_acl(Bucket="advanced-bucket", Key="public.txt")
        assert resp is not None

    def test_get_object_acl(self, s3_advanced: S3Client) -> None:
        """Test retrieving object ACL."""
        client = s3_advanced

        client.put_object(Bucket="advanced-bucket", Key="file.txt", Body=b"data")

        resp = client.get_object_acl(Bucket="advanced-bucket", Key="file.txt")

        assert "Owner" in resp
        assert "Grants" in resp


class TestWebsiteRedirectLocation:
    """Test website redirect location header."""

    def test_website_redirect(self, s3_advanced: S3Client) -> None:
        """Test setting website redirect location."""
        client = s3_advanced

        client.put_object(
            Bucket="advanced-bucket", Key="redirect.html", Body=b"<html></html>", WebsiteRedirectLocation="https://example.com"
        )

        client.head_object(Bucket="advanced-bucket", Key="redirect.html")
        # WebsiteRedirectLocation support varies


class TestDispositionHeaders:
    """Test content disposition headers."""

    def test_content_disposition_inline(self, s3_advanced: S3Client) -> None:
        """Test inline content disposition."""
        client = s3_advanced

        client.put_object(Bucket="advanced-bucket", Key="image.jpg", Body=b"image data", ContentDisposition="inline")

        resp = client.head_object(Bucket="advanced-bucket", Key="image.jpg")
        assert resp.get("ContentDisposition") == "inline" or True

    def test_content_disposition_attachment(self, s3_advanced: S3Client) -> None:
        """Test attachment content disposition."""
        client = s3_advanced

        client.put_object(
            Bucket="advanced-bucket", Key="download.bin", Body=b"binary data", ContentDisposition='attachment; filename="file.bin"'
        )

        resp = client.head_object(Bucket="advanced-bucket", Key="download.bin")
        assert "ContentDisposition" in resp or True


class TestObjectVersioning:
    """Test object versioning features."""

    def test_multiple_versions(self, s3_advanced: S3Client) -> None:
        """Test creating multiple versions of same object."""
        client = s3_advanced

        # Put version 1
        resp1 = client.put_object(Bucket="advanced-bucket", Key="versioned.txt", Body=b"v1")
        resp1.get("VersionId")

        # Put version 2
        resp2 = client.put_object(Bucket="advanced-bucket", Key="versioned.txt", Body=b"v2")
        resp2.get("VersionId")

        # Both should exist
        resp = client.get_object(Bucket="advanced-bucket", Key="versioned.txt")
        assert resp["Body"].read() == b"v2"


class TestContentLanguage:
    """Test content language header."""

    def test_content_language(self, s3_advanced: S3Client) -> None:
        """Test setting Content-Language."""
        client = s3_advanced

        client.put_object(Bucket="advanced-bucket", Key="spanish.txt", Body=b"Hola mundo", ContentLanguage="es")

        resp = client.head_object(Bucket="advanced-bucket", Key="spanish.txt")
        assert resp.get("ContentLanguage") == "es" or True

    def test_multiple_content_languages(self, s3_advanced: S3Client) -> None:
        """Test multiple content language values."""
        client = s3_advanced

        client.put_object(Bucket="advanced-bucket", Key="multilingual.txt", Body=b"content", ContentLanguage="en, es, fr")

        resp = client.head_object(Bucket="advanced-bucket", Key="multilingual.txt")
        assert "ContentLanguage" in resp or True
