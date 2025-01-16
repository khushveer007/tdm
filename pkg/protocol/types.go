package protocol

import "time"

type FileInfo struct {
	Size         int64
	Resumable    bool
	Filename     string
	ContentType  string
	LastModified string
	ETag         string
}

// DownloadOptions provides configuration options for the download process.
// It allows customization of headers, timeouts, retry behavior, and connection limits.
type DownloadOptions struct {
	Headers        map[string]string // Custom headers to be sent with the request
	Timeout        time.Duration     // Maximum time to wait for the download
	RetryCount     int               // Number of retry attempts on failure
	MaxConnections int               // Maximum number of concurrent connections
}
