package factory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/NamanBalaji/tdm/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockProtocol implements protocol.Protocol for testing
type MockProtocol struct {
	supportsFunc   func(string) bool
	initializeFunc func(context.Context, string, protocol.DownloadOptions) (*protocol.FileInfo, error)
}

func (m *MockProtocol) Initialize(ctx context.Context, url string, opts protocol.DownloadOptions) (*protocol.FileInfo, error) {
	if m.initializeFunc != nil {
		return m.initializeFunc(ctx, url, opts)
	}
	return &protocol.FileInfo{}, nil
}

func (m *MockProtocol) CreateDownloader(ctx context.Context, url string) (protocol.Downloader, error) {
	return nil, nil
}

func (m *MockProtocol) Supports(url string) bool {
	if m.supportsFunc != nil {
		return m.supportsFunc(url)
	}
	return true
}

func (m *MockProtocol) Cleanup() error {
	return nil
}

func TestFactoryRegistration(t *testing.T) {
	t.Run("successful registration", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{})
		builder := ProtocolBuilder{
			Builder: func() protocol.Protocol {
				return &MockProtocol{}
			},
			Priority: 1,
		}

		err := f.Register(protocol.ProtocolHTTP, builder)
		require.NoError(t, err)
		assert.True(t, f.IsRegistered(protocol.ProtocolHTTP))
	})

	t.Run("duplicate registration without overwrite", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{AllowOverwrite: false})
		builder := ProtocolBuilder{
			Builder: func() protocol.Protocol {
				return &MockProtocol{}
			},
			Priority: 1,
		}

		err := f.Register(protocol.ProtocolHTTP, builder)
		require.NoError(t, err)

		err = f.Register(protocol.ProtocolHTTP, builder)
		assert.ErrorIs(t, err, ErrDuplicateProtocol)
	})

	t.Run("duplicate registration with overwrite", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{AllowOverwrite: true})
		builder := ProtocolBuilder{
			Builder: func() protocol.Protocol {
				return &MockProtocol{}
			},
			Priority: 1,
		}

		err := f.Register(protocol.ProtocolHTTP, builder)
		require.NoError(t, err)

		err = f.Register(protocol.ProtocolHTTP, builder)
		assert.NoError(t, err)
	})

	t.Run("nil builder", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{})
		err := f.Register(protocol.ProtocolHTTP, ProtocolBuilder{})
		assert.Error(t, err)
	})
}

func TestFactoryCreate(t *testing.T) {
	t.Run("successful creation", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{DefaultTimeout: time.Second})
		expectedFileInfo := &protocol.FileInfo{
			Size:      1000,
			Resumable: true,
			Filename:  "test.txt",
		}

		builder := ProtocolBuilder{
			Builder: func() protocol.Protocol {
				return &MockProtocol{
					initializeFunc: func(ctx context.Context, url string, opts protocol.DownloadOptions) (*protocol.FileInfo, error) {
						return expectedFileInfo, nil
					},
				}
			},
			Priority: 1,
		}

		err := f.Register(protocol.ProtocolHTTP, builder)
		require.NoError(t, err)

		proto, fileInfo, err := f.Create(context.Background(), "http://example.com",
			protocol.WithTimeout(2*time.Second),
			protocol.WithRetryCount(3))

		require.NoError(t, err)
		assert.NotNil(t, proto)
		assert.Equal(t, expectedFileInfo, fileInfo)
	})

	t.Run("invalid URL", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{})
		proto, fileInfo, err := f.Create(context.Background(), "invalid-url")
		assert.ErrorIs(t, err, ErrInvalidURL)
		assert.Nil(t, proto)
		assert.Nil(t, fileInfo)
	})

	t.Run("unsupported protocol", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{})
		proto, fileInfo, err := f.Create(context.Background(), "unsupported://example.com")
		assert.ErrorIs(t, err, ErrUnsupportedProtocol)
		assert.Nil(t, proto)
		assert.Nil(t, fileInfo)
	})

	t.Run("initialization error", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{})
		initErr := errors.New("init error")

		builder := ProtocolBuilder{
			Builder: func() protocol.Protocol {
				return &MockProtocol{
					initializeFunc: func(context.Context, string, protocol.DownloadOptions) (*protocol.FileInfo, error) {
						return nil, initErr
					},
				}
			},
			Priority: 1,
		}

		err := f.Register(protocol.ProtocolHTTP, builder)
		require.NoError(t, err)

		proto, fileInfo, err := f.Create(context.Background(), "http://example.com")
		assert.ErrorIs(t, err, initErr)
		assert.Nil(t, proto)
		assert.Nil(t, fileInfo)
	})
}

func TestFactoryPriority(t *testing.T) {
	t.Run("highest priority selected", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{})
		expectedFileInfo := &protocol.FileInfo{Size: 1000}

		lowPriority := ProtocolBuilder{
			Builder: func() protocol.Protocol {
				return &MockProtocol{
					supportsFunc: func(string) bool { return true },
					initializeFunc: func(context.Context, string, protocol.DownloadOptions) (*protocol.FileInfo, error) {
						return expectedFileInfo, nil
					},
				}
			},
			Priority: 1,
		}

		highPriority := ProtocolBuilder{
			Builder: func() protocol.Protocol {
				return &MockProtocol{
					supportsFunc: func(string) bool { return true },
					initializeFunc: func(context.Context, string, protocol.DownloadOptions) (*protocol.FileInfo, error) {
						return expectedFileInfo, nil
					},
				}
			},
			Priority: 2,
		}

		err := f.Register(protocol.ProtocolHTTP, lowPriority)
		require.NoError(t, err)

		err = f.Register(protocol.ProtocolHTTPS, highPriority)
		require.NoError(t, err)

		proto, fileInfo, err := f.Create(context.Background(), "http://example.com")
		assert.NoError(t, err)
		assert.NotNil(t, proto)
		assert.Equal(t, expectedFileInfo, fileInfo)
	})
}

func TestFactoryConcurrency(t *testing.T) {
	t.Run("concurrent registration and creation", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{})
		var wg sync.WaitGroup
		protocols := []protocol.ProtocolType{protocol.ProtocolHTTP, protocol.ProtocolFTP, protocol.ProtocolSFTP}

		// Concurrent registration
		for _, p := range protocols {
			wg.Add(1)
			go func(proto protocol.ProtocolType) {
				defer wg.Done()
				builder := ProtocolBuilder{
					Builder: func() protocol.Protocol {
						return &MockProtocol{}
					},
					Priority: 1,
				}
				_ = f.Register(proto, builder)
			}(p)
		}

		// Concurrent creation
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, _ = f.Create(context.Background(), "http://example.com")
			}()
		}

		wg.Wait()
	})
}

func TestFactoryOptions(t *testing.T) {
	t.Run("options are applied", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{DefaultTimeout: time.Second})

		var appliedOpts protocol.DownloadOptions
		expectedFileInfo := &protocol.FileInfo{Size: 1000}

		builder := ProtocolBuilder{
			Builder: func() protocol.Protocol {
				return &MockProtocol{
					initializeFunc: func(ctx context.Context, url string, opts protocol.DownloadOptions) (*protocol.FileInfo, error) {
						appliedOpts = opts
						return expectedFileInfo, nil
					},
				}
			},
			Priority: 1,
		}

		err := f.Register(protocol.ProtocolHTTP, builder)
		require.NoError(t, err)

		timeout := 2 * time.Second
		retryCount := 5
		maxConn := 3

		_, fileInfo, err := f.Create(context.Background(), "http://example.com",
			protocol.WithTimeout(timeout),
			protocol.WithRetryCount(retryCount),
			protocol.WithMaxConnections(maxConn))

		require.NoError(t, err)
		assert.Equal(t, expectedFileInfo, fileInfo)
		assert.Equal(t, timeout, appliedOpts.Timeout)
		assert.Equal(t, retryCount, appliedOpts.RetryCount)
		assert.Equal(t, maxConn, appliedOpts.MaxConnections)
	})
}

func TestListProtocols(t *testing.T) {
	t.Run("list registered protocols", func(t *testing.T) {
		f := NewDefaultFactory(FactoryOptions{})
		builder := ProtocolBuilder{
			Builder: func() protocol.Protocol {
				return &MockProtocol{}
			},
			Priority: 1,
		}

		protocols := []protocol.ProtocolType{protocol.ProtocolHTTP, protocol.ProtocolFTP}
		for _, p := range protocols {
			err := f.Register(p, builder)
			require.NoError(t, err)
		}

		registered := f.ListProtocols()
		assert.Equal(t, len(protocols), len(registered))
		for _, p := range protocols {
			assert.Contains(t, registered, p)
		}
	})
}
