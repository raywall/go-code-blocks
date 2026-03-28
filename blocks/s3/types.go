package s3

import (
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/raywall/go-code-blocks/internal/awscfg"
)

// Block is an S3 integration block that exposes a clean object storage API.
type Block struct {
	name    string
	cfg     blockConfig
	aws     *awscfg.Resolver
	client  *awss3.Client
	presign *awss3.PresignClient
}

// ObjectMetadata holds selected metadata returned alongside a GetObject response.
type ObjectMetadata struct {
	ContentType   string
	ContentLength int64
	ETag          string
	LastModified  time.Time
}

// ObjectInfo represents a summary of an object returned by ListObjects.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
}

// PutOption configures a PutObject call.
type PutOption func(*putOptions)

type putOptions struct {
	contentType string
	metadata    map[string]string
}

// WithContentType sets the Content-Type header for uploaded objects.
func WithContentType(ct string) PutOption {
	return func(o *putOptions) { o.contentType = ct }
}

// WithMetadata attaches custom metadata key-value pairs to an uploaded object.
func WithMetadata(m map[string]string) PutOption {
	return func(o *putOptions) { o.metadata = m }
}
