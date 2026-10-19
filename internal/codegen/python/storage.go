package python

import "github.com/abdul-hamid-achik/blueprint/internal/codegen"

// genStoragePy emits src/lib/storage.py for `storage s3`. It mirrors the Node
// target's src/lib/storage.ts: upload() stores the body under a random UUID key
// with no content type and returns an s3:// URL, and the AWS_* settings are the
// same variable names the AWS SDKs read.
func (g *Generator) genStoragePy() codegen.OutputFile {
	content := fileHeader(g.sourceFile) + `import uuid
from dataclasses import dataclass
from urllib.parse import urlsplit

import boto3

from src.lib.env import env

# Unset AWS_* values fall back to boto3's default credential and region chain,
# as the Node target's S3 client does.
s3 = boto3.client(
    "s3",
    region_name=env.AWS_REGION,
    endpoint_url=env.AWS_ENDPOINT_URL,
    aws_access_key_id=env.AWS_ACCESS_KEY_ID,
    aws_secret_access_key=env.AWS_SECRET_ACCESS_KEY,
    aws_session_token=env.AWS_SESSION_TOKEN,
)


@dataclass(frozen=True)
class StoredObject:
    """Result of upload(): the s3:// URL and the object key."""

    url: str
    key: str


def upload(body: str | bytes, bucket: str, key: str | None = None) -> StoredObject:
    """Store body (text is sent as UTF-8) and return its s3:// URL."""
    object_key = key if key is not None else str(uuid.uuid4())
    data = body.encode("utf-8") if isinstance(body, str) else body
    # The Node SDK stores application/octet-stream when no type is given; match it.
    s3.put_object(Bucket=bucket, Key=object_key, Body=data, ContentType="application/octet-stream")
    return StoredObject(url=f"s3://{bucket}/{object_key}", key=object_key)


def download(url: str) -> bytes:
    """Return the bytes of the object at an s3:// URL."""
    parts = urlsplit(url)
    key = parts.path.lstrip("/")
    if parts.scheme != "s3" or not parts.netloc or not key:
        raise ValueError(f"not an s3:// object URL: {url}")
    res = s3.get_object(Bucket=parts.netloc, Key=key)
    return res["Body"].read()
`
	return codegen.OutputFile{Path: "src/lib/storage.py", Content: []byte(content)}
}
