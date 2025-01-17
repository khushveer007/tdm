package factory

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"sync"

	"github.com/NamanBalaji/tdm/pkg/protocol"
)

var (
	ErrUnsupportedProtocol = errors.New("unsupported protocol")
	ErrInvalidURL          = errors.New("invalid URL")
	ErrDuplicateProtocol   = errors.New("protocol already registered")
)

// Factory manages the creation and registration of download protocols.
// It provides thread-safe operations for registering protocol builders
// and creating protocol instances based on URLs.
type Factory interface {
	// Register adds a new protocol builder to the factory.
	// If the protocol already exists and AllowOverwrite is false,
	// it returns ErrDuplicateProtocol.
	Register(protocol protocol.ProtocolType, builder ProtocolBuilder) error

	// Create instantiates a new protocol instance for the given URL.
	// It selects the appropriate protocol based on the URL scheme and
	// protocol priorities. Options can be provided to customize the
	// protocol behavior.
	Create(ctx context.Context, rawURL string, opts ...protocol.Option) (protocol.Protocol, *protocol.FileInfo, error)

	// IsRegistered checks if a protocol type has been registered with the factory.
	IsRegistered(protocol protocol.ProtocolType) bool

	// ListProtocols returns a slice of all registered protocol types.
	ListProtocols() []protocol.ProtocolType
}

type defaultFactory struct {
	protocolBuilders sync.Map // key: ProtocolType, value: ProtocolBuilder
	options          FactoryOptions
}

func NewDefaultFactory(opts FactoryOptions) Factory {
	return &defaultFactory{
		options: opts,
	}
}

func (f *defaultFactory) Register(protocolType protocol.ProtocolType, builder ProtocolBuilder) error {
	if string(protocolType) == "" || builder.Builder == nil {
		return fmt.Errorf("cannot register empty protocol type or nil builder")
	}

	if !f.options.AllowOverwrite {
		if _, exists := f.protocolBuilders.Load(protocolType); exists {
			return fmt.Errorf("%w: %s", ErrDuplicateProtocol, protocolType)
		}
	}

	f.protocolBuilders.Store(protocolType, builder)
	return nil
}

func (f *defaultFactory) Create(ctx context.Context, rawURL string, opts ...protocol.Option) (protocol.Protocol, *protocol.FileInfo, error) {
	downloadOpts := protocol.DownloadOptions{
		Timeout:        f.options.DefaultTimeout,
		RetryCount:     3,
		MaxConnections: 1,
		Headers:        make(map[string]string),
	}

	for _, opt := range opts {
		opt(&downloadOpts)
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Scheme == "" {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	// Find all matching protocols
	var matches []struct {
		proto   protocol.ProtocolType
		builder ProtocolBuilder
	}

	f.protocolBuilders.Range(func(key, value interface{}) bool {
		protoType := key.(protocol.ProtocolType)
		builder := value.(ProtocolBuilder)
		// Create temporary protocol to check support
		proto := builder.Builder()
		if proto.Supports(rawURL) {
			matches = append(matches, struct {
				proto   protocol.ProtocolType
				builder ProtocolBuilder
			}{protoType, builder})
		}
		return true
	})

	if len(matches) == 0 {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, parsedURL.Scheme)
	}

	// Sort by priority
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].builder.Priority > matches[j].builder.Priority
	})

	// Use highest priority protocol
	proto := matches[0].builder.Builder()

	// Initialize with timeout from options
	if downloadOpts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, downloadOpts.Timeout)
		defer cancel()
	}

	fileInfo, err := proto.Initialize(ctx, rawURL, downloadOpts)
	if err != nil {
		return nil, nil, protocol.NewProtocolError(string(matches[0].proto), "Initialize", err)
	}

	return proto, fileInfo, nil
}

func (f *defaultFactory) IsRegistered(protocolType protocol.ProtocolType) bool {
	_, ok := f.protocolBuilders.Load(protocolType)
	return ok
}

func (f *defaultFactory) ListProtocols() []protocol.ProtocolType {
	var protocols []protocol.ProtocolType
	f.protocolBuilders.Range(func(key, value interface{}) bool {
		protocols = append(protocols, key.(protocol.ProtocolType))
		return true
	})
	return protocols
}
