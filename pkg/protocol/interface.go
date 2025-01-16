package protocol

import (
	"context"
	"io"
)

type Protocol interface {
	// Initialize fetches metadata, validates the URL, and prepares
	// any resources needed for downloading. For example, in HTTP
	// it might issue a HEAD request to get the file size.
	Initialize(ctx context.Context, url string, opts DownloadOptions) (*FileInfo, error)

	// CreateDownloader provides a Downloader instance that can actually
	// retrieve the data. Different protocols might implement chunking,
	// partial downloads, or specialized logic here.
	CreateDownloader(ctx context.Context, url string) (Downloader, error)

	// Supports checks if the given URL or scheme is handled by
	// this protocol implementation. For example, an HTTP protocol
	// might return true if the URL starts with "http://" or "https://".
	Supports(url string) bool

	// Cleanup performs any necessary resource release.
	// Some protocols may not need to do anything; for others,
	// it might close open connections, free buffers, etc.
	Cleanup() error
}

// Downloader is a high-level interface that focuses purely on retrieving
// the file’s content.
type Downloader interface {
	// Start initiates the actual data transfer. In a simple scenario,
	// this might return an io.ReadCloser that the caller can read from.
	Start() (io.ReadCloser, error)

	Close() error
}
